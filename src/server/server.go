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
	webhookMux.HandleFunc("/github", handlers.Webhook)
	webhookMux.HandleFunc("/agent/register/{hostname}", handlers.AgentRegister)
	webhookMux.HandleFunc("/agent/unregister/{hostname}", handlers.AgentUnregister)

	var webhookServer = &http.Server{
		Addr:    config.AppConfig.ListenAddress,
		Handler: webhookMux,
	}

	go func() {
		log.Println("Starting webhook server on", config.AppConfig.ListenAddress)
		err := webhookServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
			return
		}
	}()

	sig := <-sigs
	logger.Info("Got signal ", sig)
}
