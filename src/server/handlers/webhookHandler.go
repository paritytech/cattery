package handlers

import (
	"cattery/lib/config"
	"cattery/lib/repositories"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"fmt"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
	"net/http"
)

var logger = log.WithFields(log.Fields{
	"name": "server",
})

var traysStore = repositories.NewMemTrayRepository()

func Webhook(responseWriter http.ResponseWriter, r *http.Request) {

	var logger = logger.WithField("action", "Webhook")
	var webhookData *github.WorkflowJobEvent

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.Header.Get("X-GitHub-Event") != "workflow_job" {
		logger.Debugf("Ignoring webhook request: X-GitHub-Event is not 'workflow_job'")
		return
	}

	var organizationName = r.PathValue("org")
	var org = config.AppConfig.GetGitHubOrg(organizationName)
	if org == nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in config", organizationName)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	payload, err := github.ValidatePayload(r, []byte(org.WebhookSecret))
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

	logger.Tracef("Event payload: %v", payload)

	logger = logger.WithField("runId", webhookData.WorkflowJob.GetID())

	logger.Debugf("Action: %s", webhookData.GetAction())

	switch webhookData.GetAction() {
	case "queued":
		handleQueuedWorkflowJob(responseWriter, logger, webhookData)
	case "in_progress":
		handleInProgressWorkflowJob(responseWriter, logger, webhookData)
	case "completed":
		handleCompletedWorkflowJob(responseWriter, logger, webhookData)
	default:
		logger.Debugf("Ignoring action: '%s', for job '%s'", webhookData.GetAction(), *webhookData.WorkflowJob.Name)
		return
	}
}

// handleCompletedWorkflowJob
// handles the 'completed' action of the workflow job event
func handleCompletedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, webhookData *github.WorkflowJobEvent) {

	var tray, _ = traysStore.Get(webhookData.WorkflowJob.GetRunnerName())
	if tray == nil {
		logger.Debugf("Tray '%s' not found", webhookData.WorkflowJob.GetRunnerName())
		return
	}

	provider, err := providers.GetProvider(tray.Provider())
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get provider '%s' for tray '%s': %v", tray.Provider(), tray.Id(), err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	err = provider.CleanTray(tray)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to clean tray '%s': %v", tray.Id(), err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	_ = traysStore.Delete(tray.Id())
}

// handleInProgressWorkflowJob
// handles the 'in_progress' action of the workflow job event
func handleInProgressWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, webhookData *github.WorkflowJobEvent) {

	var tray, _ = traysStore.Get(webhookData.WorkflowJob.GetRunnerName())
	if tray == nil {
		logger.Debugf("Tray '%s' not found", webhookData.WorkflowJob.GetRunnerName())
		return
	}

	tray.JobRunId = webhookData.WorkflowJob.GetID()

	logger.Debugf("Tray '%s' is running '%s'", tray.Id(), *webhookData.WorkflowJob.Name)
}

// handleQueuedWorkflowJob
// handles the 'handleQueuedWorkflowJob' action of the workflow job event
func handleQueuedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, webhookData *github.WorkflowJobEvent) {

	var (
		trayType     *config.TrayType
		trayTypeName string
	)

	// find tray type based on labels (runs_on)
	// TODO: handle multiple labels
	for _, label := range webhookData.WorkflowJob.Labels {
		if val := config.AppConfig.GetTrayType(label); val != nil {
			trayType = val
			trayTypeName = label
			break
		}
	}

	if trayType == nil {
		logger.Debugf("Ignoring action: '%s', for job '%s', no tray type found for labels: %v", webhookData.GetAction(), *webhookData.WorkflowJob.Name, webhookData.WorkflowJob.Labels)
		return
	}

	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		var errMsg = "Error getting provider for tray type: " + trayType.Provider
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	var organizationName = webhookData.GetOrg().GetName()
	tray := trays.NewTray(
		trayTypeName,
		organizationName,
		trayType.RunnerGroupId,
		trayType.Shutdown,
		webhookData.WorkflowJob.Labels,
		*trayType)

	_ = traysStore.Save(tray)

	err = provider.RunTray(tray)
	if err != nil {
		logger.Errorf("Error creating tray for provider: %s, tray: %s: %v", tray.Provider(), tray.Id(), err)
		http.Error(responseWriter, "Error creating tray", http.StatusInternalServerError)
		return
	}

	logger.Infof("Run tray %s", tray.Id())
}
