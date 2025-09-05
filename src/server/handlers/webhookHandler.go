package handlers

import (
	"cattery/lib/config"
	"cattery/lib/jobs"
	"fmt"
	"net/http"

	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
)

func Webhook(responseWriter http.ResponseWriter, r *http.Request) {

	var logger = log.WithFields(
		log.Fields{
			"handler": "webhook",
			"call":    "Webhook",
		},
	)

	logger.Tracef("Webhook received")

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event != "workflow_job" && event != "workflow_run" {
		logger.Debugf("Ignoring webhook request: X-GitHub-Event is not 'workflow_job' or 'workflow_run', got '%s'", event)
		return
	}
	switch event {
	case "workflow_job":
		handleWorkflowJobWebhook(responseWriter, r, logger)
	case "workflow_run":
		handleWorkflowRunWebhook(responseWriter, r, logger)
	}
}
func handleWorkflowJobWebhook(responseWriter http.ResponseWriter, r *http.Request, logger *log.Entry) {
	var webhookData *github.WorkflowJobEvent

	var organizationName = r.PathValue("org")
	var org = config.AppConfig.GetGitHubOrg(organizationName)
	if org == nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in config", organizationName)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}
	logger = logger.WithField("githubOrg", organizationName)

	payload, err := github.ValidatePayload(r, []byte(org.WebhookSecret))
	if err != nil {
		logger.Errorf("Failed to validate payload: %v", err)
		http.Error(responseWriter, "Failed to validate payload", http.StatusBadRequest)
		return
	}

	hook, err := github.ParseWebHook(r.Header.Get("X-GitHub-Event"), payload)
	if err != nil {
		logger.Errorf("Failed to parse webhook: %v", err)
		return
	}
	webhookData, ok := hook.(*github.WorkflowJobEvent)
	if !ok {
		logger.Errorf("Webhook payload is not WorkflowJobEvent")
		return
	}

	logger.Tracef("Event payload: %v", payload)

	trayType := getTrayType(webhookData)
	if trayType == nil {
		logger.Tracef("Ignoring action: '%s', for job '%s', no tray type found for labels: %v", webhookData.GetAction(), *webhookData.WorkflowJob.Name, webhookData.WorkflowJob.Labels)
		return
	}
	logger = logger.WithField("jobRunId", webhookData.WorkflowJob.GetID())

	logger.Debugf("Action: %s", webhookData.GetAction())

	job := jobs.FromGithubModel(webhookData)
	job.TrayType = trayType.Name

	logger = logger.WithField("trayType", trayType.Name)

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

func handleWorkflowRunWebhook(responseWriter http.ResponseWriter, r *http.Request, logger *log.Entry) {
	logger.Debugf("Received workflow_run webhook")
	var webhookData *github.WorkflowRunEvent
	organizationName := r.PathValue("org")
	org := config.AppConfig.GetGitHubOrg(organizationName)
	if org == nil {
		errMsg := fmt.Sprintf("Organization '%s' not found in config", organizationName)
		logger.Error(errMsg)
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
		http.Error(responseWriter, "Error parsing webhook", http.StatusBadRequest)
		return
	}
	webhookData, ok := hook.(*github.WorkflowRunEvent)
	if !ok {
		logger.Errorf("Webhook payload is not WorkflowRunEvent")
		http.Error(responseWriter, "Webhook payload is not WorkflowRunEvent", http.StatusBadRequest)
		return
	}
	conclusion := webhookData.GetWorkflowRun().GetConclusion()
	repoName := webhookData.GetRepo().GetName()
	orgName := webhookData.GetOrg().GetLogin()
	logger.Debugf("Action: %s, Org: %s, Repo: %s, Workflow run ID: %d, conclusion: %s", webhookData.GetAction(), orgName, repoName, webhookData.GetWorkflowRun().GetID(), conclusion)

	// On "completed" action and "failure" conlcustion trigger restart
	if webhookData.GetAction() == "completed" && conclusion == "failure" {
		logger.Infof("Requesting restart for failed jobs in workflow run ID: %d", webhookData.GetWorkflowRun().GetID())
		err := RestartManager.Restart(*webhookData.WorkflowRun.ID, orgName, repoName)
		if err != nil {
			logger.Errorf("Failed to request restart: %v", err)
			http.Error(responseWriter, "Failed to request restart", http.StatusInternalServerError)
		}
		return
	}
}

// handleCompletedWorkflowJob
// handles the 'completed' action of the workflow job event
func handleCompletedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {

	//err := QueueManager.UpdateJobStatus(job.Id, jobs.JobStatusFinished)
	//if err != nil {
	//	logger.Errorf("Failed to update job status: %v", err)
	//}

	_, err := TrayManager.DeleteTray(job.RunnerName)
	if err != nil {
		logger.Errorf("Failed to delete tray: %v", err)
	}
}

// handleInProgressWorkflowJob
// handles the 'in_progress' action of the workflow job event
func handleInProgressWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {

	err := QueueManager.JobInProgress(job.Id)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to mark job '%s/%s' as in progress: %v", job.WorkflowName, job.Name, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
	}

	tray, err := TrayManager.SetJob(job.RunnerName, job.Id, job.WorkflowId)
	if tray == nil {
		logger.Errorf("Failed to set job '%s/%s' as in progress to tray, tray not found: %v", job.WorkflowName, job.Name, err)
	}
	if err != nil {
		logger.Errorf("Failed to set job '%s/%s' as in progress to tray: %v", job.WorkflowName, job.Name, err)
	}

	logger.Infof("Tray '%s' is running '%s/%s/%s/%s'",
		job.RunnerName,
		job.Organization, job.Repository, job.WorkflowName, job.Name,
	)
}

// handleQueuedWorkflowJob
// handles the 'handleQueuedWorkflowJob' action of the workflow job event
func handleQueuedWorkflowJob(responseWriter http.ResponseWriter, logger *log.Entry, job *jobs.Job) {
	err := QueueManager.AddJob(job)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to enqueue job '%s/%s/%s': %v", job.Repository, job.WorkflowName, job.Name, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	logger.Infof("Enqueued job %s/%s/%s/%s ", job.Organization, job.Repository, job.WorkflowName, job.Name)
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
