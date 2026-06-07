package handlers

import (
	"cattery/lib/restarter"
	"cattery/lib/scaleSetClient"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trayManager"
	"net/http"
)

type Handlers struct {
	TrayManager     *trayManager.TrayManager
	RestartManager  *restarter.WorkflowRestarter
	ScaleSetManager *scaleSetPoller.Manager
	// JitRegistry serves JIT runner configs per tray type, independent of which
	// replica holds the scale set session (see scaleSetClient.JitRegistry).
	JitRegistry *scaleSetClient.JitRegistry
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("cattery\n"))
}

func (h *Handlers) StatusIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/status", http.StatusFound)
}

func (h *Handlers) Healthcheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
