package server

import (
	"cattery/lib/config"
	"cattery/lib/restarter"
	restarterRepo "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetClient"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"cattery/lib/trays/repositories"
	"cattery/server/handlers"
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
	signal.Notify(sigs, syscall.SIGINT)
	signal.Notify(sigs, syscall.SIGTERM)
	signal.Notify(sigs, syscall.SIGKILL)

	var mux = http.NewServeMux()
	mux.HandleFunc("/{$}", handlers.Index)
	mux.HandleFunc("GET /agent/register/{id}", handlers.AgentRegister)
	mux.HandleFunc("POST /agent/unregister/{id}", handlers.AgentUnregister)
	mux.HandleFunc("GET /agent/download", handlers.AgentDownloadBinary)
	mux.HandleFunc("POST /agent/interrupt/{id}", handlers.AgentInterrupt)
	mux.HandleFunc("POST /agent/ping/{id}", handlers.AgentPing)
	mux.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)

	var httpServer = &http.Server{
		Addr:    config.AppConfig.Server.ListenAddress,
		Handler: mux,
	}

	// Db connection
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().
		ApplyURI(config.AppConfig.Database.Uri).
		SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		logger.Fatal(err)
	}

	err = client.Ping(context.Background(), nil)
	if err != nil {
		logger.Errorf("Failed to connect to MongoDB: %v", err)
		os.Exit(1)
	}

	var database = client.Database(config.AppConfig.Database.Database)

	// Initialize tray manager and repository
	var trayRepository = repositories.NewMongodbTrayRepository()
	trayRepository.Connect(database.Collection("trays"))
	handlers.TrayManager = trayManager.NewTrayManager(trayRepository)

	// Initialize restarter
	var restartManagerRepository = restarterRepo.NewMongodbRestarterRepository()
	restartManagerRepository.Connect(database.Collection("restarters"))
	handlers.RestartManager = restarter.NewWorkflowRestarter(restartManagerRepository)

	// Initialize scale set pollers — one per TrayType
	handlers.ScaleSetManager = scaleSetPoller.NewManager()
	for _, trayType := range config.AppConfig.TrayTypes {
		org := config.AppConfig.GetGitHubOrg(trayType.GitHubOrg)
		if org == nil {
			logger.Fatalf("GitHub organization '%s' not found for tray type '%s'", trayType.GitHubOrg, trayType.Name)
		}

		ssClient, err := scaleSetClient.NewScaleSetClient(org, trayType)
		if err != nil {
			logger.Fatalf("Failed to create scale set client for tray type '%s': %v", trayType.Name, err)
		}

		poller := scaleSetPoller.NewPoller(ssClient, trayType, handlers.TrayManager)
		handlers.ScaleSetManager.Register(trayType.Name, poller)

		go func(p *scaleSetPoller.Poller, name string) {
			if err := p.Run(ctx); err != nil {
				logger.Errorf("Scale set poller for '%s' exited with error: %v", name, err)
			}
		}(poller, trayType.Name)
	}

	// Start restart poller (replaces workflow_run webhook)
	handlers.RestartManager.StartPoller(ctx)

	// Start stale tray cleanup
	handlers.TrayManager.HandleStale(ctx)

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
}
