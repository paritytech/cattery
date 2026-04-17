package handlers

import (
	"cattery/lib/config"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trays"
	"cattery/ui"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

var statusTmpl = template.Must(
	template.New("status.html").
		Funcs(template.FuncMap{
			"age": func(t time.Time) string {
				d := time.Since(t)
				switch {
				case d < time.Minute:
					return d.Round(time.Second).String()
				case d < time.Hour:
					return d.Round(time.Minute).String()
				default:
					return d.Round(time.Hour).String()
				}
			},
			"joburl": func(t *trays.Tray) string {
				return jobURL(t)
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

	cfg := config.Get()
	data := struct {
		Now       time.Time
		Trays     []*trays.Tray
		Messages  []*scaleSetPoller.Message
		Orgs      []*config.GitHubOrganization
		Providers []*config.ProviderConfig
		TrayTypes []*config.TrayType
	}{
		Now:       time.Now().UTC(),
		Trays:     trayList,
		Messages:  h.ScaleSetManager.MessageHistory(),
		Orgs:      cfg.Github,
		Providers: cfg.Providers,
		TrayTypes: cfg.TrayTypes,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTmpl.Execute(w, data); err != nil {
		log.Errorf("Status: template error: %v", err)
	}
}

type statusTrayJSON struct {
	Id            string `json:"id"`
	TrayTypeName  string `json:"type"`
	GitHubOrgName string `json:"org"`
	Status        string `json:"status"`
	Repository    string `json:"repository"`
	WorkflowName  string `json:"workflow"`
	JobName       string `json:"job"`
	JobURL        string `json:"jobUrl"`
	Since         string `json:"since"`
}

type statusMessageJSON struct {
	Time     string `json:"time"`
	TrayType string `json:"type"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

func (h *Handlers) StatusData(w http.ResponseWriter, r *http.Request) {
	trayList, err := h.TrayManager.ListTrays(r.Context())
	if err != nil {
		log.Errorf("StatusData: failed to list trays: %v", err)
		http.Error(w, "failed to list trays", http.StatusInternalServerError)
		return
	}

	trayItems := make([]statusTrayJSON, len(trayList))
	for i, t := range trayList {
		trayItems[i] = statusTrayJSON{
			Id:            t.Id,
			TrayTypeName:  t.TrayTypeName,
			GitHubOrgName: t.GitHubOrgName,
			Status:        t.Status.String(),
			Repository:    t.Repository,
			WorkflowName:  t.WorkflowName,
			JobName:       t.JobName,
			JobURL:        jobURL(t),
			Since:         formatAge(t.StatusChanged),
		}
	}

	msgs := h.ScaleSetManager.MessageHistory()
	msgItems := make([]statusMessageJSON, len(msgs))
	for i, m := range msgs {
		msgItems[i] = statusMessageJSON{
			Time:     m.Time.UTC().Format("15:04:05"),
			TrayType: m.TrayType,
			Kind:     string(m.Kind),
			Detail:   m.Detail,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Now      string              `json:"now"`
		Trays    []statusTrayJSON    `json:"trays"`
		Messages []statusMessageJSON `json:"messages"`
	}{
		Now:      time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Trays:    trayItems,
		Messages: msgItems,
	})
}

// jobURL builds a GitHub Actions job URL when repository and job run ID are known.
// Format: https://github.com/{owner}/{repo}/actions/runs/{workflowRunId}/job/{jobRunId}
func jobURL(t *trays.Tray) string {
	if t.Repository == "" || t.WorkflowRunId == 0 || t.JobRunId == 0 {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", t.Repository, t.WorkflowRunId, t.JobRunId)
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Hour).String()
	}
}
