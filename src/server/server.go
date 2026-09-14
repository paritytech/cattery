package server

import (
	"cattery/lib/config"
	"cattery/lib/election"
	"cattery/lib/metrics"
	"cattery/lib/restarter"
	restarterRepo "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetClient"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"cattery/server/handlers"
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func Start() {
	var logger = log.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Db connection
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().
		ApplyURI(config.Get().Database.Uri).
		SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		logger.Fatal(err)
	}

	{
		timeoutCtx, cf := context.WithTimeout(context.Background(), 3*time.Second)
		defer cf()

		err = client.Ping(timeoutCtx, nil)
		if err != nil {
			logger.Errorf("Failed to connect to MongoDB: %v", err)
			os.Exit(1)
		}
	}

	var database = client.Database(config.Get().Database.Database)

	// Initialize tray manager and repository
	var trayRepository = repositories.NewMongodbTrayRepository()
	trayRepository.Connect(database.Collection("trays"))
	tm := trayManager.NewTrayManager(trayRepository, providers.DefaultFactory{})

	// Register DB-backed metrics collector
	metrics.RegisterTrayCollector(tm)

	// Initialize restarter
	var restartManagerRepository = restarterRepo.NewMongodbRestarterRepository()
	restartManagerRepository.Connect(database.Collection("restarters"))
	rm := restarter.NewWorkflowRestarter(restartManagerRepository)

	// Initialize scale set pollers — one per TrayType.
	// The JIT registry is populated alongside, so the agent register handler can
	// generate JIT configs on any replica without depending on the (leader-only)
	// poller for that tray type.
	ssm := scaleSetPoller.NewManager()
	jitRegistry := scaleSetClient.NewJitRegistry()

	// Leader election decides which replica runs each tray type's poller. The
	// tray HTTP plane (served via jitRegistry above) runs on every replica
	// regardless, so trays stay served during rollouts and failovers.
	elector, err := election.NewFromConfig(config.Get().Coordination.WithDefaults(), database.Collection("leases"))
	if err != nil {
		logger.Fatalf("Failed to initialize leader election: %v", err)
	}

	// Every leader-only background loop is tracked here so shutdown waits for
	// all of them to step down and release their leases.
	var leaderTasks sync.WaitGroup

	for _, trayType := range config.Get().TrayTypes {
		org := config.Get().GetGitHubOrg(trayType.GitHubOrg)
		if org == nil {
			logger.Fatalf("GitHub organization '%s' not found for tray type '%s'", trayType.GitHubOrg, trayType.Name)
		}

		ssClient, err := scaleSetClient.NewScaleSetClient(org, trayType)
		if err != nil {
			logger.Fatalf("Failed to create scale set client for tray type '%s': %v", trayType.Name, err)
		}
		jitRegistry.Register(trayType.Name, ssClient)

		poller := scaleSetPoller.NewPoller(ssClient, trayType, tm)
		ssm.Register(trayType.Name, poller)

		// Run the poller only while this replica holds the lease for this
		// tray type; leaderCtx is cancelled the moment leadership is lost.
		name := trayType.Name
		runLeaderTask(ctx, &leaderTasks, elector, name, logger, func(leaderCtx context.Context) {
			runPoller(leaderCtx, poller, name, logger)
		})
	}

	// The restart poller and the stale tray cleanup are cluster-wide singletons:
	// two replicas running them would re-run the same workflows and clean the
	// same trays twice. Each is leased under its own key, independent of the
	// per-tray-type poller leases, so they may land on any replica.
	runLeaderTask(ctx, &leaderTasks, elector, leaseKeyRestarter, logger, rm.RunPoller)
	runLeaderTask(ctx, &leaderTasks, elector, leaseKeyStaleTrays, logger, tm.HandleStale)

	h := &handlers.Handlers{
		TrayManager:     tm,
		RestartManager:  rm,
		ScaleSetManager: ssm,
		JitRegistry:     jitRegistry,
	}

	servers := startServers(logger, cancel, h)

	select {
	case sig := <-sigs:
		logger.Info("Got signal ", sig)
	case <-ctx.Done():
		logger.Info("Context cancelled, shutting down")
	}
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Errorf("HTTP server shutdown error: %v", err)
		}
	}

	logger.Info("Waiting for leader tasks to shut down...")
	leaderTasks.Wait()
	logger.Info("All leader tasks stopped")

	disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer disconnectCancel()
	if err := client.Disconnect(disconnectCtx); err != nil {
		logger.Errorf("Failed to disconnect from MongoDB: %v", err)
	}
	logger.Info("MongoDB connection closed")
}

// Lease keys for the cluster-wide singleton loops. They share the key space
// with tray type names, so tray types must not use these names.
const (
	leaseKeyRestarter  = "cattery-restarter"
	leaseKeyStaleTrays = "cattery-stale-trays"
)

// runLeaderTask runs task in the background for as long as this replica holds
// the lease for key: task receives a context that is cancelled the moment
// leadership is lost and is re-invoked whenever leadership is regained. The
// goroutine is tracked in wg so shutdown can wait for the task to step down.
func runLeaderTask(ctx context.Context, wg *sync.WaitGroup, elector election.Elector, key string, logger *log.Logger, task election.OnElected) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := elector.Run(ctx, key, task); err != nil && ctx.Err() == nil {
			logger.Errorf("Leader election for '%s' exited: %v", key, err)
		}
	}()
}

// runPoller runs a tray type's scale set listener until ctx is cancelled —
// either leadership was lost (leaderCtx) or the process is shutting down —
// restarting the listener after transient errors.
func runPoller(ctx context.Context, p *scaleSetPoller.Poller, name string, logger *log.Logger) {
	for {
		if err := p.Run(ctx); err != nil {
			if ctx.Err() != nil {
				logger.Infof("Scale set poller for '%s' stopped: %v", name, err)
				return
			}
			logger.Errorf("Scale set poller for '%s' exited with error: %v — restarting in 30s", name, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		return
	}
}

func agentMux(h *handlers.Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", h.Index)
	mux.HandleFunc("GET /healthcheck", h.Healthcheck)
	mux.HandleFunc("GET /agent/register/{id}", h.AgentRegister)
	mux.HandleFunc("POST /agent/unregister/{id}", h.AgentUnregister)
	mux.HandleFunc("GET /agent/download", handlers.AgentDownloadBinary)
	mux.HandleFunc("POST /agent/interrupt/{id}", h.AgentInterrupt)
	mux.HandleFunc("POST /agent/ping/{id}", h.AgentPing)
	return mux
}

func registerStatusRoutes(mux *http.ServeMux, h *handlers.Handlers) {
	mux.HandleFunc("/status", h.Status)
	mux.HandleFunc("GET /status/data", h.StatusData)
	mux.Handle("/metrics", promhttp.Handler())
}

func listenAndServe(logger *log.Logger, cancel context.CancelFunc, addr string, handler http.Handler) *http.Server {
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		logger.Infof("Starting server on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("HTTP server on %s failed: %v", addr, err)
			cancel()
		}
	}()
	return srv
}

// startServers starts the agent server and the status+metrics server.
// If statusListenAddress is unset or matches the agent address, status and
// metrics are served on the same port as the agent endpoints.
func startServers(logger *log.Logger, cancel context.CancelFunc, h *handlers.Handlers) []*http.Server {
	mainAddr := config.Get().Server.ListenAddress
	statusAddr := config.Get().Server.StatusListenAddress

	aMux := agentMux(h)

	if statusAddr == "" || statusAddr == mainAddr {
		registerStatusRoutes(aMux, h)
		return []*http.Server{listenAndServe(logger, cancel, mainAddr, aMux)}
	}

	sMux := http.NewServeMux()
	sMux.HandleFunc("/{$}", h.StatusIndex)
	sMux.HandleFunc("GET /healthcheck", h.Healthcheck)
	registerStatusRoutes(sMux, h)
	return []*http.Server{
		listenAndServe(logger, cancel, mainAddr, aMux),
		listenAndServe(logger, cancel, statusAddr, sMux),
	}
}
