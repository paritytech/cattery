package githubClient

import (
	"cattery/lib/config"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v84/github"
	log "github.com/sirupsen/logrus"
)

const githubAPITimeout = 30 * time.Second

var (
	githubClientsMu sync.Mutex
	githubClients   = make(map[string]*github.Client)
)

type GithubClient struct {
	client *github.Client
	Org    *config.GitHubOrganization
}

// NewGithubClient wraps an already-constructed go-github client. Tests use it
// to point the client at a fake API server; production code should use
// NewGithubClientWithOrgName.
func NewGithubClient(client *github.Client, org *config.GitHubOrganization) *GithubClient {
	return &GithubClient{
		client: client,
		Org:    org,
	}
}

func NewGithubClientWithOrgName(orgName string) (*GithubClient, error) {

	orgConfig := config.Get().GetGitHubOrg(orgName)
	if orgConfig == nil {
		return nil, errors.New("GitHub organization not found")
	}

	client, err := createClient(orgConfig)
	if err != nil {
		return nil, err
	}

	return &GithubClient{
		client: client,
		Org:    orgConfig,
	}, nil
}

func (gc *GithubClient) RestartFailedJobs(repoName string, workflowId int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	_, err := gc.client.Actions.RerunFailedJobsByID(ctx, gc.Org.Name, repoName, workflowId)
	return err
}

// ErrRunnerNotFound is returned by GetRunnerState when GitHub has no runner
// with the given ID. A runner GitHub does not know about cannot be running a
// job.
var ErrRunnerNotFound = errors.New("runner not found")

// RunnerState is what GitHub reports about a self-hosted runner.
//
// Busy is only meaningful while Online: a runner whose VM has vanished can sit
// at busy=true until GitHub reaps the registration, so callers must not read
// an offline busy runner as "a job is running".
type RunnerState struct {
	Busy   bool
	Online bool
}

// GetRunnerState reports what GitHub currently knows about a runner.
//
// This is the authoritative answer to "is this tray doing work?", and unlike
// cattery's own tray status it does not depend on the scale set listener
// having received a JobStarted message. The stale-tray reaper and the agent
// ping handler both consult it before destroying a tray, so a listener that
// has gone blind cannot cause a running job to be killed.
//
// Requires the GitHub App to hold organization "Self-hosted runners: read",
// which it already needs to manage runner scale sets. Without it the call
// returns 403, which IsForbidden reports; callers must treat that as
// "unknown", never as "not busy".
func (gc *GithubClient) GetRunnerState(runnerId int64) (RunnerState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	runner, resp, err := gc.client.Actions.GetOrganizationRunner(ctx, gc.Org.Name, runnerId)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return RunnerState{}, ErrRunnerNotFound
		}
		return RunnerState{}, err
	}

	return RunnerState{
		Busy:   runner.GetBusy(),
		Online: strings.EqualFold(runner.GetStatus(), "online"),
	}, nil
}

type WorkflowRunInfo struct {
	Status     string
	Conclusion string
	Event      string
	HeadBranch string
	WorkflowID int64
	CreatedAt  time.Time
}

func (gc *GithubClient) GetWorkflowRunInfo(repoName string, workflowRunId int64) (WorkflowRunInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	wr, _, err := gc.client.Actions.GetWorkflowRunByID(ctx, gc.Org.Name, repoName, workflowRunId)
	if err != nil {
		return WorkflowRunInfo{}, err
	}
	return WorkflowRunInfo{
		Status:     wr.GetStatus(),
		Conclusion: wr.GetConclusion(),
		Event:      wr.GetEvent(),
		HeadBranch: wr.GetHeadBranch(),
		WorkflowID: wr.GetWorkflowID(),
		CreatedAt:  wr.GetCreatedAt().Time,
	}, nil
}

// HasNewerWorkflowRun reports whether the workflow has a run for the same
// branch and event created after the given run. Run IDs are monotonically
// increasing, so the newest run having a higher ID means the given run has
// been superseded.
func (gc *GithubClient) HasNewerWorkflowRun(repoName string, workflowID int64, branch string, event string, runID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	runs, _, err := gc.client.Actions.ListWorkflowRunsByID(ctx, gc.Org.Name, repoName, workflowID, &github.ListWorkflowRunsOptions{
		Branch:              branch,
		Event:               event,
		ExcludePullRequests: true,
		ListOptions:         github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return false, err
	}

	// Runs are listed newest-first.
	for _, run := range runs.WorkflowRuns {
		if run.GetID() > runID {
			return true, nil
		}
	}
	return false, nil
}

// CancellationCause classifies why a workflow run concluded "cancelled".
type CancellationCause string

const (
	// CancellationCauseUnknown means no cancelled job carries an annotation
	// naming a cause; for runs that lost a runner to preemption this is the
	// preemption itself.
	CancellationCauseUnknown CancellationCause = ""
	// CancellationCauseUser means a person cancelled the run.
	CancellationCauseUser CancellationCause = "user"
	// CancellationCauseConcurrency means a newer run in the same concurrency
	// group cancelled the run.
	CancellationCauseConcurrency CancellationCause = "concurrency"
)

// GetRunCancellationCause inspects the annotations GitHub attaches to the
// cancelled jobs of a run. A manual cancellation annotates every in-progress
// job with "The run was canceled by @user." (actions/runner#4411) and a
// concurrency-group cancellation with "Canceling since a higher priority
// waiting request for '<group>' exists"; a job that merely lost its runner
// gets "The runner has received a shutdown signal..." or "...lost
// communication with the server" instead (actions/runner#2662, #3539).
// The first matching annotation decides. Requires the App to have
// 'Checks: read'.
func (gc *GithubClient) GetRunCancellationCause(repoName string, runID int64) (CancellationCause, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	jobOpts := &github.ListWorkflowJobsOptions{
		Filter:      "latest",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		jobs, resp, err := gc.client.Actions.ListWorkflowJobs(ctx, gc.Org.Name, repoName, runID, jobOpts)
		if err != nil {
			return CancellationCauseUnknown, err
		}
		for _, job := range jobs.Jobs {
			if job.GetConclusion() != "cancelled" {
				continue
			}
			cause, err := gc.jobCancellationCause(ctx, repoName, job.GetID())
			if err != nil {
				return CancellationCauseUnknown, err
			}
			if cause != CancellationCauseUnknown {
				return cause, nil
			}
		}
		if resp.NextPage == 0 {
			return CancellationCauseUnknown, nil
		}
		jobOpts.Page = resp.NextPage
	}
}

// jobCancellationCause scans the check-run annotations of one job (job IDs are
// check-run IDs) for a cancellation notice.
func (gc *GithubClient) jobCancellationCause(ctx context.Context, repoName string, jobID int64) (CancellationCause, error) {
	opts := &github.ListOptions{PerPage: 100}
	for {
		annotations, resp, err := gc.client.Checks.ListCheckRunAnnotations(ctx, gc.Org.Name, repoName, jobID, opts)
		if err != nil {
			return CancellationCauseUnknown, err
		}
		for _, a := range annotations {
			log.Debugf("Job %d annotation: %q", jobID, a.GetMessage())
			if cause := classifyCancellationAnnotation(a.GetMessage()); cause != CancellationCauseUnknown {
				return cause, nil
			}
		}
		if resp.NextPage == 0 {
			return CancellationCauseUnknown, nil
		}
		opts.Page = resp.NextPage
	}
}

func classifyCancellationAnnotation(message string) CancellationCause {
	msg := strings.ToLower(message)
	switch {
	case strings.Contains(msg, "canceled by @"), strings.Contains(msg, "cancelled by @"):
		return CancellationCauseUser
	case strings.Contains(msg, "higher priority waiting request"):
		return CancellationCauseConcurrency
	default:
		return CancellationCauseUnknown
	}
}

// HasClosedPullRequestForBranch reports whether a pull request with the given
// head branch was closed (merged or not) after the given time, while no pull
// request for that branch is currently open. Detection is by head branch
// rather than commit SHA because squash/rebase merges never put the run's head
// SHA on the default branch.
func (gc *GithubClient) HasClosedPullRequestForBranch(repoName string, headBranch string, closedAfter time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	prs, _, err := gc.client.PullRequests.List(ctx, gc.Org.Name, repoName, &github.PullRequestListOptions{
		State: "all",
		Head:  gc.Org.Name + ":" + headBranch,
	})
	if err != nil {
		return false, err
	}

	// An open PR anywhere in the list vetoes the check; the list is ordered by
	// creation date, so open and closed PRs can appear in any order.
	for _, pr := range prs {
		if pr.GetState() == "open" {
			return false, nil
		}
	}
	for _, pr := range prs {
		if pr.ClosedAt != nil && pr.ClosedAt.Time.After(closedAfter) {
			return true, nil
		}
	}
	return false, nil
}

// IsForbidden reports whether err is a GitHub API 403 response, which for an
// installation token means the App lacks the required permission.
func IsForbidden(err error) bool {
	var ghErr *github.ErrorResponse
	return errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusForbidden
}

// createClient creates a new GitHub client
func createClient(org *config.GitHubOrganization) (*github.Client, error) {
	githubClientsMu.Lock()
	defer githubClientsMu.Unlock()

	if githubClient, ok := githubClients[org.Name]; ok {
		return githubClient, nil
	}

	tr := http.DefaultTransport

	itr, err := ghinstallation.NewKeyFromFile(
		tr,
		org.AppId,
		org.InstallationId,
		org.PrivateKeyPath,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to load GitHub App private key for org %s: %w", org.Name, err)
	}

	// Use installation transport with github.com/google/go-github
	client := github.NewClient(&http.Client{Transport: itr})

	githubClients[org.Name] = client

	return client, nil
}
