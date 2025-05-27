package server

import (
	"cattery/lib/config"
	"cattery/server/handlers"
	log "github.com/sirupsen/logrus"
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
	webhookMux.HandleFunc("/{$}", func(writer http.ResponseWriter, request *http.Request) {
		return
	})
	webhookMux.HandleFunc("GET /agent/register/{id}", handlers.AgentRegister)
	webhookMux.HandleFunc("POST /agent/unregister/{id}", handlers.AgentUnregister)

	webhookMux.HandleFunc("POST /github/{org}", handlers.Webhook)

	var webhookServer = &http.Server{
		Addr:    config.AppConfig.Server.ListenAddress,
		Handler: webhookMux,
	}

	err := handlers.QueueManager.Connect(config.AppConfig.Database.Uri)
	if err != nil {
		logger.Errorf("Error connecting to database: %v", err)
	}

	err = handlers.QueueManager.Load()
	if err != nil {
		logger.Errorf("Error loading queue manager: %v", err)
	}

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
