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
	"net/http"
	"os"
	"os/signal"
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
		ApplyURI(config.AppConfig.Database.Uri).
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

	var database = client.Database(config.AppConfig.Database.Database)

	// Initialize tray manager and repository
	var trayRepository = repositories.NewMongodbTrayRepository()
	trayRepository.Connect(database.Collection("trays"))
	tm := trayManager.NewTrayManager(trayRepository, providers.DefaultFactory{})

	// Initialize restarter
	var restartManagerRepository = restarterRepo.NewMongodbRestarterRepository()
	restartManagerRepository.Connect(database.Collection("restarters"))
	rm := restarter.NewWorkflowRestarter(restartManagerRepository)

	// Initialize scale set pollers — one per TrayType
	ssm := scaleSetPoller.NewManager()
	for _, trayType := range config.AppConfig.TrayTypes {
		org := config.AppConfig.GetGitHubOrg(trayType.GitHubOrg)
		if org == nil {
			logger.Fatalf("GitHub organization '%s' not found for tray type '%s'", trayType.GitHubOrg, trayType.Name)
		}

		ssClient, err := scaleSetClient.NewScaleSetClient(org, trayType)
		if err != nil {
			logger.Fatalf("Failed to create scale set client for tray type '%s': %v", trayType.Name, err)
		}

		poller := scaleSetPoller.NewPoller(ssClient, trayType, tm)
		ssm.Register(trayType.Name, poller)

		ssm.Wg.Add(1)
		go func(p *scaleSetPoller.Poller, name string) {
			defer ssm.Wg.Done()
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

	var mux = http.NewServeMux()
	mux.HandleFunc("/{$}", h.Index)
	mux.HandleFunc("GET /agent/register/{id}", h.AgentRegister)
	mux.HandleFunc("POST /agent/unregister/{id}", h.AgentUnregister)
	mux.HandleFunc("GET /agent/download", handlers.AgentDownloadBinary)
	mux.HandleFunc("POST /agent/interrupt/{id}", h.AgentInterrupt)
	mux.HandleFunc("POST /agent/ping/{id}", h.AgentPing)
	mux.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)

	var httpServer = &http.Server{
		Addr:    config.AppConfig.Server.ListenAddress,
		Handler: mux,
	}

	// Start HTTP server
	go func() {
		logger.Infof("Starting server on %s", config.AppConfig.Server.ListenAddress)
		err := httpServer.ListenAndServe()
		if err != nil {
			logger.Fatal(err)
			return
		}
	}()

	sig := <-sigs
	logger.Info("Got signal ", sig)
	cancel()

	logger.Info("Waiting for pollers to shut down...")
	ssm.Wg.Wait()
	logger.Info("All pollers stopped")
}
