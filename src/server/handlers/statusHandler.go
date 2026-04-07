package handlers

import (
	"cattery/lib/scaleSetPoller"
	"cattery/ui"
	"html/template"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

var statusTmpl = template.Must(
	template.New("status.html").
		Funcs(template.FuncMap{
			"age": func(t time.Time) string {
				d := time.Since(t).Round(time.Second)
				switch {
				case d < time.Minute:
					return d.String()
				case d < time.Hour:
					return d.Round(time.Minute).String()
				default:
					return d.Round(time.Hour).String()
				}
			},
		}).
		ParseFS(ui.Templates, "status.html"),
)

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	trayList, err := h.TrayManager.ListTrays(r.Context())
	if err != nil {
		log.Errorf("Status: failed to list trays: %v", err)
		http.Error(w, "failed to list trays", http.StatusInternalServerError)
		return
	}

	data := struct {
		Now      time.Time
		Trays    interface{}
		Messages []*scaleSetPoller.Message
	}{
		Now:      time.Now().UTC(),
		Trays:    trayList,
		Messages: h.ScaleSetManager.MessageHistory(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTmpl.Execute(w, data); err != nil {
		log.Errorf("Status: template error: %v", err)
	}
}
