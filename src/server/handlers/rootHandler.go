package handlers

import (
	"cattery/lib/jobQueue"
	"cattery/lib/trayManager"
	"net/http"
)

var QueueManager *jobQueue.QueueManager
var TrayManager *trayManager.TrayManager

func Index(responseWriter http.ResponseWriter, r *http.Request) {
	return
}
