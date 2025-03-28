package handlers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"fmt"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
	"net/http"
)

var traysStore = make(map[string]*trays.Tray)

var logger = log.WithFields(log.Fields{
	"name": "server",
})

func Webhook(responseWriter http.ResponseWriter, r *http.Request) {

	var logger = logger.WithField("action", "Webhook")
	var webhookData *github.WorkflowJobEvent

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := github.ValidatePayload(r, []byte(config.AppConfig.WebhookSecret))
	if err != nil {
		logger.Errorf("Error validating payload: %v", err)
		http.Error(responseWriter, "Error validating payload", http.StatusBadRequest)
		return
	}

	hook, err := github.ParseWebHook(r.Header.Get("X-GitHub-Event"), payload)
	if err != nil {
		logger.Errorf("Error parsing webhook: %v", err)
		return
	}

	webhookData = hook.(*github.WorkflowJobEvent)

	if webhookData.GetAction() != "queued" {
		logger.Debugf("Ignoring action: %s for runId: %d, only 'queued' actions are supported", webhookData.GetAction(), webhookData.WorkflowJob.RunID)
		return
	}

	logger = logger.WithField("runId", webhookData.WorkflowJob.RunID)
	logger.Tracef("Event payload: %v", payload)

	var trayType *config.TrayType

	// find tray type based on labels (runs_on)
	for _, label := range webhookData.WorkflowJob.Labels {
		if val, ok := config.AppConfig.TrayTypes[label]; ok {
			trayType = &val
		}
	}

	if trayType == nil {
		logger.Debugf("Ignoring action: %s, no tray type found for labels: %v", webhookData.GetAction(), webhookData.WorkflowJob.Labels)
		return
	}

	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		var errMsg = "Error getting provider for tray type: " + trayType.Provider
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	tray := createTray(*trayType, webhookData)
	traysStore[tray.Name] = tray

	err = provider.RunTray(tray)
	if err != nil {
		logger.Errorf("Error creating tray: %v", err)
		http.Error(responseWriter, "Error creating tray", http.StatusInternalServerError)
		return
	}

}

// createTray creates a tray object from the webhook data
func createTray(trayType config.TrayType, webhookData *github.WorkflowJobEvent) *trays.Tray {
	var containerName = fmt.Sprint(trayType.Config.Get("namePrefix"), "-", *webhookData.WorkflowJob.RunID)
	var tray = &trays.Tray{
		Id:         fmt.Sprintf("%d", *webhookData.WorkflowJob.RunID),
		Name:       containerName,
		Address:    "",
		Type:       "docker",
		Provider:   trayType.Provider,
		Labels:     webhookData.WorkflowJob.Labels,
		TrayConfig: trayType.Config,
	}
	return tray
}
