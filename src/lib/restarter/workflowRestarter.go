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
	// Must exceed the longest expected workflow run: a job preempted early in
	// a run can only be re-run after the whole run completes.
	const requestTTL = 6 * time.Hour

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
	// "cancelled" is included because GitHub marks some jobs on a preempted
	// runner as cancelled rather than failed, and a single cancelled job makes
	// the whole run conclude "cancelled" — such runs are preemption victims
	// just like failed ones.
	case "failure", "cancelled":
		// A failed merge queue run already evicted the PR from the queue;
		// re-running it cannot re-enqueue the PR, so a restart is wasted.
		if run.Event == "merge_group" {
			logger.Infof("Skipping restart for workflow run %d (%s/%s): merge queue runs cannot be retried",
				req.WorkflowRunId, req.OrgName, req.RepoName)
			break
		}

		prClosed, err := wr.branchPRClosed(logger, ghClient, req, run)
		if err != nil {
			// Leave the request pending: it is retried on the next poll and
			// eventually expires via TTL.
			return
		}
		if prClosed {
			logger.Infof("Skipping restart for workflow run %d (%s/%s): pull request for branch '%s' already merged or closed",
				req.WorkflowRunId, req.OrgName, req.RepoName, run.HeadBranch)
			break
		}

		if run.Conclusion == "cancelled" {
			superseded, err := wr.runSuperseded(logger, ghClient, req, run)
			if err != nil {
				// Leave the request pending: it is retried on the next poll
				// and eventually expires via TTL.
				return
			}
			if superseded {
				logger.Infof("Skipping restart for cancelled workflow run %d (%s/%s): a newer run exists for branch '%s'",
					req.WorkflowRunId, req.OrgName, req.RepoName, run.HeadBranch)
				break
			}

			cause, err := wr.cancellationCause(logger, ghClient, req)
			if err != nil {
				// Leave the request pending: it is retried on the next poll
				// and eventually expires via TTL.
				return
			}
			if cause != githubClient.CancellationCauseUnknown {
				logger.Infof("Skipping restart for cancelled workflow run %d (%s/%s): cancelled by %s, not by preemption",
					req.WorkflowRunId, req.OrgName, req.RepoName, cause)
				break
			}
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

// cancellationCause reports who cancelled the run according to the
// annotations on its cancelled jobs: a person, a concurrency group, or nobody
// identifiable (a preemption). Restart requests only exist for runs that lost
// a runner to preemption, so an unknown cause means the preemption itself
// cancelled the run and it must be restarted.
func (wr *WorkflowRestarter) cancellationCause(logger *log.Entry, ghClient *githubClient.GithubClient, req repositories.RestartRequest) (githubClient.CancellationCause, error) {
	cause, err := ghClient.GetRunCancellationCause(req.RepoName, req.WorkflowRunId)
	if err != nil {
		// Fail open on missing permission: a restart nobody needs is better
		// than restarts silently stopping until the App grants 'Checks: read'.
		if githubClient.IsForbidden(err) {
			logger.Warnf("Cannot check cancellation cause for workflow run %d: GitHub App lacks 'Checks: read' permission, proceeding with restart", req.WorkflowRunId)
			return githubClient.CancellationCauseUnknown, nil
		}
		logger.Errorf("Failed to check cancellation cause for workflow run %d: %v", req.WorkflowRunId, err)
		return githubClient.CancellationCauseUnknown, err
	}
	return cause, nil
}

// runSuperseded reports whether a newer run of the same workflow exists for
// the run's branch and event. A cancelled run with a newer sibling was almost
// certainly cancelled by a concurrency group when the newer run started, not
// by preemption, and restarting it would waste runners on an obsolete commit.
// This check is cheap (one API call) and needs no extra permission, so it runs
// before the annotation-based cancellationCause check.
func (wr *WorkflowRestarter) runSuperseded(logger *log.Entry, ghClient *githubClient.GithubClient, req repositories.RestartRequest, run githubClient.WorkflowRunInfo) (bool, error) {
	// Without a branch or workflow id the newer-run lookup cannot be scoped;
	// fail open and restart.
	if run.HeadBranch == "" || run.WorkflowID == 0 {
		return false, nil
	}

	superseded, err := ghClient.HasNewerWorkflowRun(req.RepoName, run.WorkflowID, run.HeadBranch, run.Event, req.WorkflowRunId)
	if err != nil {
		logger.Errorf("Failed to check for newer runs of workflow run %d (branch '%s'): %v", req.WorkflowRunId, run.HeadBranch, err)
		return false, err
	}
	return superseded, nil
}

// branchPRClosed reports whether the run's head branch belongs to a pull
// request closed (merged or not) after the run was created. The closedAfter
// guard keeps an old closed PR from a reused branch name from suppressing a
// legitimate restart.
func (wr *WorkflowRestarter) branchPRClosed(logger *log.Entry, ghClient *githubClient.GithubClient, req repositories.RestartRequest, run githubClient.WorkflowRunInfo) (bool, error) {
	// Only runs triggered by a pull request are tied to its lifecycle; push,
	// schedule, and dispatch runs restart regardless of PR state.
	if run.Event != "pull_request" && run.Event != "pull_request_target" {
		return false, nil
	}

	if run.HeadBranch == "" {
		return false, nil
	}

	closed, err := ghClient.HasClosedPullRequestForBranch(req.RepoName, run.HeadBranch, run.CreatedAt)
	if err != nil {
		// Fail open on missing permission: a restart nobody needs is better
		// than restarts silently stopping until the App grants
		// 'Pull requests: read'.
		if githubClient.IsForbidden(err) {
			logger.Warnf("Cannot check merged pull requests for workflow run %d: GitHub App lacks 'Pull requests: read' permission, proceeding with restart", req.WorkflowRunId)
			return false, nil
		}
		logger.Errorf("Failed to check pull requests for workflow run %d (branch '%s'): %v", req.WorkflowRunId, run.HeadBranch, err)
		return false, err
	}
	return closed, nil
}
