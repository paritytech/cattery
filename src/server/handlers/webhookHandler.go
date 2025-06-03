package handlers

import (
	"cattery/lib/config"
	"cattery/lib/jobs"
	"fmt"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
	"net/http"
)

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

	var trayType = getTrayType(webhookData)
	if trayType == nil {
		logger.Tracef("Ignoring action: '%s', for job '%s', no tray type found for labels: %v", webhookData.GetAction(), *webhookData.WorkflowJob.Name, webhookData.WorkflowJob.Labels)
		return
	}

	logger = logger.WithField("runId", webhookData.WorkflowJob.GetID())
	logger.Debugf("Action: %s", webhookData.GetAction())

	var job = jobs.FromGithubModel(webhookData)
	job.TrayType = trayType.Name

	switch webhookData.GetAction() {
	case "queued":
		handleQueuedWorkflowJob(responseWriter, logger, job)
	case "in_progress":
		handleInProgressWorkflowJob(responseWriter, logger, job)
	case "completed":
		handleCompletedWorkflowJob(responseWriter, logger, job)
	default:
		logger.Debugf("Ignoring action: '%s', for job '%s'", webhookData.GetAction(), *webhookData.WorkflowJob.Name)
		return
	}
}

// handleCompletedWorkflowJob
// handles the 'completed' action of the workflow job event
func handleCompletedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {

	err := TrayManager.DeleteTray(job.RunnerName)
	if err != nil {
		logger.Errorf("Error deleting tray: %v", err)
	}
}

// handleInProgressWorkflowJob
// handles the 'in_progress' action of the workflow job event
func handleInProgressWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {

	err := QueueManager.JobInProgress(job.Id, job.RunnerName)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to mark job '%s/%s' as in progress: %v", job.WorkflowName, job.Name, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
	}

	logger.Infof("Tray '%s' is running '%s/%s' in '%s/%s'",
		job.RunnerName,
		job.WorkflowName, job.Name,
		job.Organization, job.Repository,
	)
}

// handleQueuedWorkflowJob
// handles the 'handleQueuedWorkflowJob' action of the workflow job event
func handleQueuedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {
	err := QueueManager.AddJob(job)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to enqueue job '%s/%s/%s': %v", job.Repository, job.WorkflowName, job.Name, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	logger.Infof("Enqueued job %s/%s/%s ", job.Repository, job.WorkflowName, job.Name)
}

func getTrayType(webhookData *github.WorkflowJobEvent) *config.TrayType {

	if len(webhookData.WorkflowJob.Labels) != 1 {
		// Cattery only support one label for now
		return nil
	}

	// find tray type based on labels (runs_on)
	var label = webhookData.WorkflowJob.Labels[0]
	var trayType = config.AppConfig.GetTrayType(label)

	if trayType == nil {
		return nil
	}

	if trayType.GitHubOrg != webhookData.GetOrg().GetLogin() {
		return nil
	}

	return trayType
}
