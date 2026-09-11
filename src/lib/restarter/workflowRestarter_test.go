package restarter

import (
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/restarter/repositories"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// --- Mock restarter repository ---

type mockRestarterRepository struct {
	requests  []repositories.RestartRequest
	saveErr   error
	deleteErr error
	getErr    error
	deleted   []int64
}

func (m *mockRestarterRepository) SaveRestartRequest(_ context.Context, workflowRunId int64, orgName string, repoName string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.requests = append(m.requests, repositories.RestartRequest{
		WorkflowRunId: workflowRunId,
		OrgName:       orgName,
		RepoName:      repoName,
		CreatedAt:     time.Now(),
	})
	return nil
}

func (m *mockRestarterRepository) DeleteRestartRequest(_ context.Context, workflowRunId int64) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, workflowRunId)
	return nil
}

func (m *mockRestarterRepository) GetAllPendingRestartRequests(_ context.Context) ([]repositories.RestartRequest, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.requests, nil
}

var _ repositories.RestarterRepository = (*mockRestarterRepository)(nil)

// --- Fake GitHub API ---

// fakeGithubAPI serves the four endpoints the restarter hits, backed by
// canned JSON responses.
type fakeGithubAPI struct {
	runJSON       string
	prsJSON       string
	prsStatus     int
	runListJSON   string
	runListStatus int
	jobsJSON      string
	jobsStatus    int
	// annotationsJSON is keyed by job id; jobs without an entry get an
	// empty annotation list.
	annotationsJSON   map[int64]string
	annotationsStatus int

	prCalls          int
	runListCalls     int
	jobsCalls        int
	annotationsCalls int
	restarts         int
}

func (f *fakeGithubAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/rerun-failed-jobs"):
		f.restarts++
		w.WriteHeader(http.StatusCreated)
	case strings.Contains(r.URL.Path, "/actions/workflows/"):
		f.runListCalls++
		if f.runListStatus != 0 {
			w.WriteHeader(f.runListStatus)
			return
		}
		_, _ = w.Write([]byte(f.runListJSON))
	case strings.HasSuffix(r.URL.Path, "/jobs"):
		f.jobsCalls++
		if f.jobsStatus != 0 {
			w.WriteHeader(f.jobsStatus)
			return
		}
		if f.jobsJSON == "" {
			_, _ = w.Write([]byte(jobsJSON()))
			return
		}
		_, _ = w.Write([]byte(f.jobsJSON))
	case strings.HasSuffix(r.URL.Path, "/annotations"):
		f.annotationsCalls++
		if f.annotationsStatus != 0 {
			w.WriteHeader(f.annotationsStatus)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		jobID, _ := strconv.ParseInt(parts[len(parts)-2], 10, 64)
		if body, ok := f.annotationsJSON[jobID]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte("[]"))
	case strings.Contains(r.URL.Path, "/actions/runs/"):
		_, _ = w.Write([]byte(f.runJSON))
	case strings.HasSuffix(r.URL.Path, "/pulls"):
		f.prCalls++
		if f.prsStatus != 0 {
			w.WriteHeader(f.prsStatus)
			return
		}
		_, _ = w.Write([]byte(f.prsJSON))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func runJSON(status, conclusion, headBranch, event string) string {
	return fmt.Sprintf(`{"id":42,"workflow_id":7,"status":%q,"conclusion":%q,"head_branch":%q,"event":%q,"created_at":%q}`,
		status, conclusion, headBranch, event, time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339))
}

func runListJSON(runIDs ...int64) string {
	var runs []string
	for _, id := range runIDs {
		runs = append(runs, fmt.Sprintf(`{"id":%d}`, id))
	}
	return fmt.Sprintf(`{"total_count":%d,"workflow_runs":[%s]}`, len(runIDs), strings.Join(runs, ","))
}

type fakeJob struct {
	id         int64
	conclusion string
}

func jobsJSON(jobs ...fakeJob) string {
	var items []string
	for _, j := range jobs {
		items = append(items, fmt.Sprintf(`{"id":%d,"run_id":42,"status":"completed","conclusion":%q}`, j.id, j.conclusion))
	}
	return fmt.Sprintf(`{"total_count":%d,"jobs":[%s]}`, len(jobs), strings.Join(items, ","))
}

func annotationsJSON(messages ...string) string {
	var items []string
	for _, m := range messages {
		items = append(items, fmt.Sprintf(`{"annotation_level":"failure","message":%q}`, m))
	}
	return "[" + strings.Join(items, ",") + "]"
}

const (
	userCancelAnnotation        = "The run was canceled by @alice."
	concurrencyCancelAnnotation = "Canceling since a higher priority waiting request for 'ci-feature' exists"
	preemptionAnnotation        = "The runner has received a shutdown signal. This can happen when the runner service is stopped, or a manually started runner is canceled."
)

func prListJSON(prs ...string) string {
	return "[" + strings.Join(prs, ",") + "]"
}

func openPR() string { return `{"number":1,"state":"open"}` }

func closedPR(closedAt time.Time) string {
	return fmt.Sprintf(`{"number":2,"state":"closed","closed_at":%q}`, closedAt.UTC().Format(time.RFC3339))
}

func mergedPR(mergedAt time.Time) string {
	ts := mergedAt.UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"number":3,"state":"closed","closed_at":%q,"merged_at":%q}`, ts, ts)
}

func newTestRestarter(t *testing.T, repo repositories.RestarterRepository, api *fakeGithubAPI) *WorkflowRestarter {
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	baseURL, err := url.Parse(srv.URL + "/")
	assert.NoError(t, err)
	client.BaseURL = baseURL

	gh := githubClient.NewGithubClient(client, &config.GitHubOrganization{Name: "org"})

	wr := NewWorkflowRestarter(repo)
	wr.newGithubClient = func(_ string) (*githubClient.GithubClient, error) {
		return gh, nil
	}
	return wr
}

func testRequest() repositories.RestartRequest {
	return repositories.RestartRequest{
		WorkflowRunId: 42,
		OrgName:       "org",
		RepoName:      "repo",
		CreatedAt:     time.Now(),
	}
}

// --- Tests ---

func TestRequestRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	wr := NewWorkflowRestarter(repo)

	err := wr.RequestRestart(context.Background(), 123, "test-org", "test-org/repo")
	assert.NoError(t, err)
	assert.Len(t, repo.requests, 1)
	assert.Equal(t, int64(123), repo.requests[0].WorkflowRunId)
	assert.Equal(t, "test-org", repo.requests[0].OrgName)
	assert.Equal(t, "test-org/repo", repo.requests[0].RepoName)
}

func TestRequestRestart_Error(t *testing.T) {
	repo := &mockRestarterRepository{saveErr: errors.New("db error")}
	wr := NewWorkflowRestarter(repo)

	err := wr.RequestRestart(context.Background(), 123, "test-org", "test-org/repo")
	assert.Error(t, err)
}

func TestPollPendingRestarts_NoRequests(t *testing.T) {
	repo := &mockRestarterRepository{}
	wr := NewWorkflowRestarter(repo)

	logger := log.WithField("test", true)
	// Should not panic or error with empty request list
	wr.pollPendingRestarts(context.Background(), logger, time.Hour)
}

func TestPollPendingRestarts_GetError(t *testing.T) {
	repo := &mockRestarterRepository{getErr: errors.New("db error")}
	wr := NewWorkflowRestarter(repo)

	logger := log.WithField("test", true)
	// Should not panic — just logs the error
	wr.pollPendingRestarts(context.Background(), logger, time.Hour)
}

func TestPollPendingRestarts_ExpiredRequest(t *testing.T) {
	repo := &mockRestarterRepository{
		requests: []repositories.RestartRequest{
			{
				WorkflowRunId: 100,
				OrgName:       "org",
				RepoName:      "org/repo",
				CreatedAt:     time.Now().Add(-2 * time.Hour), // expired
			},
		},
	}
	wr := NewWorkflowRestarter(repo)

	logger := log.WithField("test", true)
	wr.pollPendingRestarts(context.Background(), logger, time.Hour)

	// Expired request should be deleted
	assert.Contains(t, repo.deleted, int64(100))
}

func TestHandleRestartRequest_FailureRestarts(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature", "pull_request"),
		// closed long before the run existed — a reused branch name
		prsJSON: prListJSON(closedPR(time.Now().Add(-2 * time.Hour))),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_MergedPRSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature", "pull_request"),
		prsJSON: prListJSON(mergedPR(time.Now())),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Contains(t, repo.deleted, int64(42), "request must be cleaned up when the PR is already merged")
}

func TestHandleRestartRequest_ClosedUnmergedPRSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature", "pull_request"),
		prsJSON: prListJSON(closedPR(time.Now())),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Contains(t, repo.deleted, int64(42), "request must be cleaned up when the PR was closed without merging")
}

func TestHandleRestartRequest_OpenPRWinsOverClosed(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature", "pull_request"),
		// a recently closed PR alongside an open one — e.g. closed then reopened
		prsJSON: prListJSON(closedPR(time.Now()), openPR()),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts, "an open PR for the branch must keep restarts working")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_MergedCheckErrorKeepsRequest(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:   runJSON("completed", "failure", "feature", "pull_request"),
		prsStatus: http.StatusInternalServerError,
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Empty(t, repo.deleted, "request must stay pending for retry on merged-check error")
}

func TestHandleRestartRequest_MergedCheckForbiddenFailsOpen(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:   runJSON("completed", "failure", "feature", "pull_request"),
		prsStatus: http.StatusForbidden, // App lacks 'Pull requests: read'
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts, "missing permission must not block restarts")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_MergeGroupSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "gh-readonly-queue/main/pr-7", "merge_group"),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "merge queue runs must not be restarted")
	assert.Zero(t, api.prCalls)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_PushEventSkipsPRCheck(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature", "push"),
		prsJSON: prListJSON(mergedPR(time.Now())), // must not be consulted for push runs
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.prCalls, "push-triggered runs must not consult the PR endpoint")
	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_NoHeadBranchStillRestarts(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "", "pull_request"),
		prsJSON: prListJSON(mergedPR(time.Now())), // must not be consulted without a head branch
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.prCalls)
	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledRestarts(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "main", "push"),
		runListJSON: runListJSON(42), // the run itself is the newest — no supersession
		jobsJSON:    jobsJSON(fakeJob{1, "success"}, fakeJob{2, "cancelled"}),
		annotationsJSON: map[int64]string{
			2: annotationsJSON(preemptionAnnotation),
		},
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.runListCalls, "cancelled runs must be checked for supersession")
	assert.Equal(t, 1, api.annotationsCalls, "only cancelled jobs are inspected for a cancellation cause")
	assert.Equal(t, 1, api.restarts, "a cancelled run nobody cancelled is a preemption victim and must restart")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledByUserSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "feature", "pull_request"),
		prsJSON:     prListJSON(openPR()),
		runListJSON: runListJSON(42),
		jobsJSON:    jobsJSON(fakeJob{1, "cancelled"}, fakeJob{2, "cancelled"}),
		annotationsJSON: map[int64]string{
			1: annotationsJSON(userCancelAnnotation),
			2: annotationsJSON(userCancelAnnotation),
		},
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "a run cancelled by a person must not be restarted")
	assert.Equal(t, 1, api.annotationsCalls, "the first matching annotation decides")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledByUserAfterPreemptionSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "main", "push"),
		runListJSON: runListJSON(42),
		// job 1 lost its runner, then a person cancelled the rest of the run
		jobsJSON: jobsJSON(fakeJob{1, "cancelled"}, fakeJob{2, "cancelled"}),
		annotationsJSON: map[int64]string{
			1: annotationsJSON(preemptionAnnotation),
			2: annotationsJSON(userCancelAnnotation),
		},
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "a person cancelling after a preemption still vetoes the restart")
	assert.Equal(t, 2, api.annotationsCalls)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledByConcurrencyAnnotationSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "cancelled", "feature", "pull_request"),
		prsJSON: prListJSON(openPR()),
		// the newer run belongs to another event type, so the run list check misses it
		runListJSON: runListJSON(42),
		jobsJSON:    jobsJSON(fakeJob{1, "cancelled"}),
		annotationsJSON: map[int64]string{
			1: annotationsJSON(concurrencyCancelAnnotation),
		},
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "a run cancelled by its concurrency group must not be restarted")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledCauseCheckErrorKeepsRequest(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "main", "push"),
		runListJSON: runListJSON(42),
		jobsStatus:  http.StatusInternalServerError,
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Empty(t, repo.deleted, "request must stay pending for retry on cancellation-cause check error")
}

func TestHandleRestartRequest_CancelledCauseCheckForbiddenFailsOpen(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:           runJSON("completed", "cancelled", "main", "push"),
		runListJSON:       runListJSON(42),
		jobsJSON:          jobsJSON(fakeJob{1, "cancelled"}),
		annotationsStatus: http.StatusForbidden, // App lacks 'Checks: read'
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts, "missing permission must not block restarts")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledSupersededSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "feature", "pull_request"),
		prsJSON:     prListJSON(openPR()),
		runListJSON: runListJSON(43), // a newer run cancelled this one via concurrency
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "a run superseded by a newer one must not be restarted")
	assert.Zero(t, api.jobsCalls, "supersession is decided before the annotation lookup")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledSupersededCheckErrorKeepsRequest(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:       runJSON("completed", "cancelled", "main", "push"),
		runListStatus: http.StatusInternalServerError,
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Empty(t, repo.deleted, "request must stay pending for retry on supersession-check error")
}

func TestHandleRestartRequest_CancelledNoHeadBranchSkipsSupersededCheck(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "cancelled", "", "push"),
		runListJSON: runListJSON(43), // must not be consulted without a head branch
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.runListCalls, "supersession check cannot be scoped without a branch")
	assert.Equal(t, 1, api.restarts, "fail open and restart when supersession cannot be checked")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_CancelledMergeGroupSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "cancelled", "gh-readonly-queue/main/pr-7", "merge_group"),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts, "cancelled merge queue runs must not be restarted")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_FailureSkipsSupersededCheck(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:     runJSON("completed", "failure", "main", "push"),
		runListJSON: runListJSON(43), // must not be consulted for failed runs
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.runListCalls, "failed runs restart without a supersession check")
	assert.Zero(t, api.jobsCalls, "failed runs restart without a cancellation-cause check")
	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_NotCompleted(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("in_progress", "", "feature", "pull_request"),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Empty(t, repo.deleted)
}

func TestHandleRestartRequest_SuccessCleansUp(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "success", "feature", "pull_request"),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Zero(t, api.prCalls, "success runs do not consult the PR endpoint")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestNewWorkflowRestarter(t *testing.T) {
	repo := &mockRestarterRepository{}
	wr := NewWorkflowRestarter(repo)
	assert.NotNil(t, wr)
}
