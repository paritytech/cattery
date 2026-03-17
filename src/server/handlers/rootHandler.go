package handlers

import (
	"cattery/lib/restarter"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"net/http"
)

var TrayManager *trayManager.TrayManager
var RestartManager *restarter.WorkflowRestarter
var ScaleSetManager *scaleSetPoller.Manager

func Index(responseWriter http.ResponseWriter, r *http.Request) {
	return
}
