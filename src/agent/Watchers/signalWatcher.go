package Watchers

import (
	"cattery/agent/shutdownEvents"
	"cattery/lib/messages"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
)

func WatchSignal() {
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		signal.Notify(sigs, syscall.SIGTERM)
		signal.Notify(sigs, syscall.SIGKILL)

		sig := <-sigs
		log.Info("Got signal ", sig)

		shutdownEvents.Emit(messages.UnregisterReasonSigTerm, "Got signal "+sig.String())
	}()
}
