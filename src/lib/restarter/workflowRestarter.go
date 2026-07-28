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
	// newGithubClient is overridable so tests can point the client at a fake
	// GitHub API server.
	newGithubClient func(orgName string) (*githubClient.GithubClient, error)
}

func NewWorkflowRestarter(repository repositories.RestarterRepository) *WorkflowRestarter {
	return &WorkflowRestarter{
		repository:      repository,
		newGithubClient: githubClient.NewGithubClientWithOrgName,
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
			if err := wr.repository.DeleteRestartRequest(ctx, req.WorkflowRunId); err != nil {
				logger.Errorf("Failed to delete expired restart request for workflow %d: %v", req.WorkflowRunId, err)
			}
			continue
		}

		wr.handleRestartRequest(ctx, logger, req)
	}
}

func (wr *WorkflowRestarter) handleRestartRequest(ctx context.Context, logger *log.Entry, req repositories.RestartRequest) {
	ghClient, err := wr.newGithubClient(req.OrgName)
	if err != nil {
		logger.Errorf("Failed to get GitHub client for org %s: %v", req.OrgName, err)
		return
	}

	run, err := ghClient.GetWorkflowRunInfo(req.RepoName, req.WorkflowRunId)
	if err != nil {
		logger.Errorf("Failed to get workflow run status for %d: %v", req.WorkflowRunId, err)
		return
	}

	if run.Status != "completed" {
		return
	}

	switch run.Conclusion {
	case "failure":
		merged, err := wr.branchMerged(logger, ghClient, req, run)
		if err != nil {
			// Leave the request pending: it is retried on the next poll and
			// eventually expires via TTL.
			return
		}
		if merged {
			logger.Infof("Skipping restart for workflow run %d (%s/%s): pull request for branch '%s' already merged",
				req.WorkflowRunId, req.OrgName, req.RepoName, run.HeadBranch)
			break
		}

		logger.Infof("Restarting failed jobs for workflow run %d (%s/%s)", req.WorkflowRunId, req.OrgName, req.RepoName)
		err = ghClient.RestartFailedJobs(req.RepoName, req.WorkflowRunId)
		if err != nil {
			logger.Errorf("Failed to restart workflow run %d: %v", req.WorkflowRunId, err)
			return
		}
		logger.Infof("Successfully restarted failed jobs for workflow run %d", req.WorkflowRunId)
	default:
		logger.Debugf("Workflow run %d completed with conclusion '%s', cleaning up restart request", req.WorkflowRunId, run.Conclusion)
	}

	if err := wr.repository.DeleteRestartRequest(ctx, req.WorkflowRunId); err != nil {
		logger.Errorf("Failed to delete restart request for workflow %d: %v", req.WorkflowRunId, err)
	}
}

// branchMerged reports whether the run's head branch belongs to a pull request
// merged after the run was created. The mergedAfter guard keeps an old merged
// PR from a reused branch name from suppressing a legitimate restart.
func (wr *WorkflowRestarter) branchMerged(logger *log.Entry, ghClient *githubClient.GithubClient, req repositories.RestartRequest, run githubClient.WorkflowRunInfo) (bool, error) {
	if run.HeadBranch == "" {
		return false, nil
	}

	merged, err := ghClient.HasMergedPullRequestForBranch(req.RepoName, run.HeadBranch, run.CreatedAt)
	if err != nil {
		// Fail open on missing permission: a restart nobody needs is better
		// than restarts silently stopping until the App grants
		// 'Pull requests: read'.
		if githubClient.IsForbidden(err) {
			logger.Warnf("Cannot check merged pull requests for workflow run %d: GitHub App lacks 'Pull requests: read' permission, proceeding with restart", req.WorkflowRunId)
			return false, nil
		}
		logger.Errorf("Failed to check merged pull requests for workflow run %d (branch '%s'): %v", req.WorkflowRunId, run.HeadBranch, err)
		return false, err
	}
	return merged, nil
}
