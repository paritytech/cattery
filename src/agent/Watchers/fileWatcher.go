package Watchers

import (
	"cattery/agent/shutdownEvents"
	"cattery/lib/messages"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

func WatchFile() {
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Fatal(err)
		}
		defer watcher.Close()

		err = watcher.Add("./shutdown_file")
		if err != nil {
			log.Fatal(err)
		}

		var message string

		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				message = "Modified file: " + event.Name
			}
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				message = "Removed file: " + event.Name
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				message = "Renamed file: " + event.Name
			}
		case err := <-watcher.Errors:
			message = "File error: " + err.Error()
			log.Error(message)
		}

		log.Infof(message)

		shutdownEvents.Emit(messages.UnregisterReasonPreempted, message)
	}()
}
