package server

import (
	"cattery/lib/config"
	"cattery/lib/jobQueue"
	"cattery/lib/trayManager"
	"cattery/lib/trays/repositories"
	"cattery/server/handlers"
	"context"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func Start() {

	var logger = log.Logger{}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	signal.Notify(sigs, syscall.SIGTERM)
	signal.Notify(sigs, syscall.SIGKILL)

	var webhookMux = http.NewServeMux()
	webhookMux.HandleFunc("/{$}", handlers.Index)
	webhookMux.HandleFunc("GET /agent/register/{id}", handlers.AgentRegister)
	webhookMux.HandleFunc("POST /agent/unregister/{id}", handlers.AgentUnregister)

	webhookMux.HandleFunc("POST /github/{org}", handlers.Webhook)

	var webhookServer = &http.Server{
		Addr:    config.AppConfig.Server.ListenAddress,
		Handler: webhookMux,
	}

	// Db connection
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(config.AppConfig.Database.Uri).SetServerAPIOptions(serverAPI)

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

	//QueueManager initialization
	handlers.QueueManager = jobQueue.NewQueueManager(false)
	handlers.QueueManager.Connect(database.Collection("jobs"))

	err = handlers.QueueManager.Load()
	if err != nil {
		logger.Errorf("Error loading queue manager: %v", err)
	}

	handlers.TrayManager.HandleJobsQueue(context.Background(), handlers.QueueManager)
	handlers.TrayManager.HandleStale(context.Background())

	// Start the server
	go func() {
		log.Println("Starting webhook server on", config.AppConfig.Server.ListenAddress)
		err := webhookServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
			return
		}
	}()

	sig := <-sigs
	logger.Info("Got signal ", sig)
}
