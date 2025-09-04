package handlers

import (
	"cattery/lib/jobQueue"
	"cattery/lib/restarter"
	"cattery/lib/trayManager"
	"net/http"
)

var QueueManager *jobQueue.QueueManager
var TrayManager *trayManager.TrayManager
var RestartManager *restarter.WorkflowRestarter

func Index(responseWriter http.ResponseWriter, r *http.Request) {
	return
}
