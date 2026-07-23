package handlers

import (
	"cattery/lib/config"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trays"
	"cattery/lib/version"
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
			"msgJobURL": messageJobURL,
			"providerType": func(name string) string {
				if p := config.Get().GetProvider(name); p != nil {
					return p.Get("type")
				}
				return ""
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
		Version   string
		Trays     []*trays.Tray
		Messages  []*scaleSetPoller.Message
		Orgs      []*config.GitHubOrganization
		Providers []*config.ProviderConfig
		TrayTypes []*config.TrayType
	}{
		Now:       time.Now().UTC(),
		Version:   version.Get(),
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
	TimeFull string `json:"timeFull"`
	TrayType string `json:"type"`
	Kind     string `json:"kind"`

	// Job event fields.
	Repository     string `json:"repository,omitempty"`
	JobDisplayName string `json:"jobDisplayName,omitempty"`
	RunnerName     string `json:"runnerName,omitempty"`
	Result         string `json:"result,omitempty"`
	JobURL         string `json:"jobUrl,omitempty"`

	// Scale event fields.
	DesiredCount int                  `json:"desiredCount,omitempty"`
	Stats        *statusScaleStatsJSON `json:"stats,omitempty"`
}

type statusScaleStatsJSON struct {
	Available  int `json:"available"`
	Assigned   int `json:"assigned"`
	Running    int `json:"running"`
	Busy       int `json:"busy"`
	Idle       int `json:"idle"`
	Registered int `json:"registered"`
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
		item := statusMessageJSON{
			Time:     m.Time.UTC().Format("15:04:05"),
			TimeFull: m.Time.UTC().Format("2006-01-02 15:04:05 UTC"),
			TrayType: m.TrayType,
			Kind:     string(m.Kind),
		}
		if m.IsScale() {
			item.DesiredCount = m.DesiredCount
			if m.Stats != nil {
				item.Stats = &statusScaleStatsJSON{
					Available:  m.Stats.Available,
					Assigned:   m.Stats.Assigned,
					Running:    m.Stats.Running,
					Busy:       m.Stats.Busy,
					Idle:       m.Stats.Idle,
					Registered: m.Stats.Registered,
				}
			}
		} else {
			item.Repository = m.Repository
			item.JobDisplayName = m.JobDisplayName
			item.RunnerName = m.RunnerName
			item.Result = m.Result
			item.JobURL = messageJobURL(m)
		}
		msgItems[i] = item
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

// buildJobURL returns the GitHub Actions workflow run URL, or "" if any part
// is missing. The scale set messages carry only a GUID job id (not the numeric
// one GitHub's /job/{id} URLs need), so link to the run page instead.
// Format: https://github.com/{owner}/{repo}/actions/runs/{workflowRunId}
func buildJobURL(repo string, workflowRunID int64) string {
	if repo == "" || workflowRunID == 0 {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, workflowRunID)
}

func jobURL(t *trays.Tray) string {
	return buildJobURL(t.Repository, t.WorkflowRunId)
}

func messageJobURL(m *scaleSetPoller.Message) string {
	return buildJobURL(m.Repository, m.WorkflowRunID)
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
