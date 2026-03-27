package handlers

import (
	"cattery/lib/restarter"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"net/http"
)

type Handlers struct {
	TrayManager    *trayManager.TrayManager
	RestartManager *restarter.WorkflowRestarter
	ScaleSetManager *scaleSetPoller.Manager
}

func (h *Handlers) Index(responseWriter http.ResponseWriter, r *http.Request) {
	return
}
