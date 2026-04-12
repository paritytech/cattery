package server

import (
	"cattery/lib/config"
	"cattery/lib/restarter"
	restarterRepo "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetClient"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"cattery/server/handlers"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	_ "modernc.org/sqlite"
)

func Start() {
	var logger = log.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Initialize database
	var trayRepo repositories.TrayRepository
	var restarterRepo_ restarterRepo.RestarterRepository
	var dbCloser io.Closer

	switch config.Get().Database.Type {
	case "sqlite":
		db, err := sql.Open("sqlite", config.Get().Database.Path)
		if err != nil {
			logger.Fatalf("Failed to open SQLite database: %v", err)
		}
		// SQLite doesn't handle concurrent writes well without WAL mode
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			logger.Fatalf("Failed to enable WAL mode: %v", err)
		}

		tr, err := repositories.NewSqliteTrayRepository(db)
		if err != nil {
			logger.Fatalf("Failed to initialize SQLite tray repository: %v", err)
		}
		rr, err := restarterRepo.NewSqliteRestarterRepository(db)
		if err != nil {
			logger.Fatalf("Failed to initialize SQLite restarter repository: %v", err)
		}
		trayRepo = tr
		restarterRepo_ = rr
		dbCloser = db
		logger.Infof("Using SQLite database: %s", config.Get().Database.Path)

	default: // mongodb
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
				logger.Fatalf("Failed to connect to MongoDB: %v", err)
			}
		}

		database := client.Database(config.Get().Database.Database)

		mongoTrayRepo := repositories.NewMongodbTrayRepository()
		mongoTrayRepo.Connect(database.Collection("trays"))
		trayRepo = mongoTrayRepo

		mongoRestarterRepo := restarterRepo.NewMongodbRestarterRepository()
		mongoRestarterRepo.Connect(database.Collection("restarters"))
		restarterRepo_ = mongoRestarterRepo

		dbCloser = mongoCloser{client}
		logger.Info("Using MongoDB database")
	}

	tm := trayManager.NewTrayManager(trayRepo, providers.DefaultFactory{})
	rm := restarter.NewWorkflowRestarter(restarterRepo_)

	// Initialize scale set pollers — one per TrayType
	ssm := scaleSetPoller.NewManager()
	for _, trayType := range config.Get().TrayTypes {
		org := config.Get().GetGitHubOrg(trayType.GitHubOrg)
		if org == nil {
			logger.Fatalf("GitHub organization '%s' not found for tray type '%s'", trayType.GitHubOrg, trayType.Name)
		}

		ssClient, err := scaleSetClient.NewScaleSetClient(org, trayType)
		if err != nil {
			logger.Fatalf("Failed to create scale set client for tray type '%s': %v", trayType.Name, err)
		}

		poller := scaleSetPoller.NewPoller(ssClient, trayType, tm)
		ssm.Register(trayType.Name, poller)

		ssm.Add(1)
		go func(p *scaleSetPoller.Poller, name string) {
			defer ssm.Done()
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
		}(poller, trayType.Name)
	}

	// Start restart poller (replaces workflow_run webhook)
	rm.StartPoller(ctx)

	// Start stale tray cleanup
	tm.HandleStale(ctx)

	h := &handlers.Handlers{
		TrayManager:     tm,
		RestartManager:  rm,
		ScaleSetManager: ssm,
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

	logger.Info("Waiting for pollers to shut down...")
	ssm.Wait()
	logger.Info("All pollers stopped")

	if err := dbCloser.Close(); err != nil {
		logger.Errorf("Failed to close database: %v", err)
	}
	logger.Info("Database connection closed")
}

// mongoCloser adapts mongo.Client to io.Closer.
type mongoCloser struct {
	client *mongo.Client
}

func (m mongoCloser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Disconnect(ctx)
}

func agentMux(h *handlers.Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", h.Index)
	mux.HandleFunc("GET /agent/register/{id}", h.AgentRegister)
	mux.HandleFunc("POST /agent/unregister/{id}", h.AgentUnregister)
	mux.HandleFunc("GET /agent/download", handlers.AgentDownloadBinary)
	mux.HandleFunc("POST /agent/interrupt/{id}", h.AgentInterrupt)
	mux.HandleFunc("POST /agent/ping/{id}", h.AgentPing)
	return mux
}

func registerStatusRoutes(mux *http.ServeMux, h *handlers.Handlers) {
	mux.HandleFunc("/status", h.Status)
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
	registerStatusRoutes(sMux, h)
	return []*http.Server{
		listenAndServe(logger, cancel, mainAddr, aMux),
		listenAndServe(logger, cancel, statusAddr, sMux),
	}
}
