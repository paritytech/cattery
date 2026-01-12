package Watchers

import (
	"cattery/agent/shutdownEvents"
	"cattery/lib/messages"
	"context"
	"os"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

var filename = "./shutdown_file"

func WatchFile(ctx context.Context) {
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Fatal(err)
		}
		defer watcher.Close()

		createFile(filename)

		err = watcher.Add(filename)
		if err != nil {
			log.Fatal(err)
		}

		var message string

		if ctx == nil {
			ctx = context.Background()
		}

		select {
		case <-ctx.Done():
			return
		case event := <-watcher.Events:
			if event.Op.Has(fsnotify.Write) {
				message = "Modified file: " + event.Name
			}
			if event.Op.Has(fsnotify.Remove) {
				message = "Removed file: " + event.Name
			}
			if event.Op.Has(fsnotify.Rename) {
				message = "Renamed file: " + event.Name
			}
		case err := <-watcher.Errors:
			message = "File error: " + err.Error()
			log.Error(message)
		}

		log.Info(message)

		shutdownEvents.Emit(messages.UnregisterReasonPreempted, message)
	}()
}

func createFile(filename string) {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Fatal(err)
	}
	f.Close()
}
