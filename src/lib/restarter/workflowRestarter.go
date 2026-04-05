package restarter

import (
	"cattery/lib/githubClient"
	"cattery/lib/restarter/repositories"
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

type WorkflowRestarter struct {
	repository repositories.RestarterRepository
}

func NewWorkflowRestarter(repository repositories.RestarterRepository) *WorkflowRestarter {
	return &WorkflowRestarter{
		repository: repository,
	}
}

func (wr *WorkflowRestarter) RequestRestart(ctx context.Context, workflowRunId int64, orgName string, repoName string) error {
	log.Debugf("Requesting restart for workflow run id %d (%s/%s)", workflowRunId, orgName, repoName)
	return wr.repository.SaveRestartRequest(ctx, workflowRunId, orgName, repoName)
}

// StartPoller starts a background goroutine that periodically checks pending restart
// requests and triggers restarts when workflows have completed with failure.
func (wr *WorkflowRestarter) StartPoller(ctx context.Context) {
	const pollInterval = 30 * time.Second
	const requestTTL = 1 * time.Hour

	logger := log.WithField("component", "restarterPoller")

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("Restart poller shutting down")
				return
			default:
				time.Sleep(pollInterval)
				wr.pollPendingRestarts(ctx, logger, requestTTL)
			}
		}
	}()

	logger.Info("Restart poller started")
}

func (wr *WorkflowRestarter) pollPendingRestarts(ctx context.Context, logger *log.Entry, ttl time.Duration) {
	requests, err := wr.repository.GetAllPendingRestartRequests(ctx)
	if err != nil {
		logger.Errorf("Failed to get pending restart requests: %v", err)
		return
	}

	for _, req := range requests {
		if time.Since(req.CreatedAt) > ttl {
			logger.Warnf("Restart request for workflow %d expired (age: %v), deleting", req.WorkflowRunId, time.Since(req.CreatedAt))
			_ = wr.repository.DeleteRestartRequest(ctx, req.WorkflowRunId)
			continue
		}

		wr.handleRestartRequest(ctx, logger, req)
	}
}

func (wr *WorkflowRestarter) handleRestartRequest(ctx context.Context, logger *log.Entry, req repositories.RestartRequest) {
	ghClient, err := githubClient.NewGithubClientWithOrgName(req.OrgName)
	if err != nil {
		logger.Errorf("Failed to get GitHub client for org %s: %v", req.OrgName, err)
		return
	}

	status, conclusion, err := ghClient.GetWorkflowRunStatus(req.RepoName, req.WorkflowRunId)
	if err != nil {
		logger.Errorf("Failed to get workflow run status for %d: %v", req.WorkflowRunId, err)
		return
	}

	if status != "completed" {
		return
	}

	switch conclusion {
	case "failure":
		logger.Infof("Restarting failed jobs for workflow run %d (%s/%s)", req.WorkflowRunId, req.OrgName, req.RepoName)
		err = ghClient.RestartFailedJobs(req.RepoName, req.WorkflowRunId)
		if err != nil {
			logger.Errorf("Failed to restart workflow run %d: %v", req.WorkflowRunId, err)
			return
		}
		logger.Infof("Successfully restarted failed jobs for workflow run %d", req.WorkflowRunId)
	default:
		logger.Debugf("Workflow run %d completed with conclusion '%s', cleaning up restart request", req.WorkflowRunId, conclusion)
	}

	if err := wr.repository.DeleteRestartRequest(ctx, req.WorkflowRunId); err != nil {
		logger.Errorf("Failed to delete restart request for workflow %d: %v", req.WorkflowRunId, err)
	}
}
