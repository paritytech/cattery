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

// fakeGithubAPI serves the three endpoints the restarter hits, backed by
// canned JSON responses.
type fakeGithubAPI struct {
	runJSON   string
	prsJSON   string
	prsStatus int

	prCalls  int
	restarts int
}

func (f *fakeGithubAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/rerun-failed-jobs"):
		f.restarts++
		w.WriteHeader(http.StatusCreated)
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

func runJSON(status, conclusion, headBranch string) string {
	return fmt.Sprintf(`{"id":42,"status":%q,"conclusion":%q,"head_branch":%q,"created_at":%q}`,
		status, conclusion, headBranch, time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339))
}

const mergedPRJSON = `[{"number":1,"state":"closed"}]`

func mergedNowPRJSON() string {
	return fmt.Sprintf(`[{"number":1,"state":"closed","merged_at":%q}]`, time.Now().UTC().Format(time.RFC3339))
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
		runJSON: runJSON("completed", "failure", "feature"),
		prsJSON: mergedPRJSON, // closed but not merged
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_MergedPRSkipsRestart(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", "feature"),
		prsJSON: mergedNowPRJSON(),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Contains(t, repo.deleted, int64(42), "request must be cleaned up when the PR is already merged")
}

func TestHandleRestartRequest_MergedCheckErrorKeepsRequest(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON:   runJSON("completed", "failure", "feature"),
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
		runJSON:   runJSON("completed", "failure", "feature"),
		prsStatus: http.StatusForbidden, // App lacks 'Pull requests: read'
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Equal(t, 1, api.restarts, "missing permission must not block restarts")
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_NoHeadBranchStillRestarts(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "failure", ""),
		prsJSON: mergedNowPRJSON(), // must not be consulted without a head branch
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.prCalls)
	assert.Equal(t, 1, api.restarts)
	assert.Contains(t, repo.deleted, int64(42))
}

func TestHandleRestartRequest_NotCompleted(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("in_progress", "", "feature"),
	}
	wr := newTestRestarter(t, repo, api)

	wr.handleRestartRequest(context.Background(), log.WithField("test", true), testRequest())

	assert.Zero(t, api.restarts)
	assert.Empty(t, repo.deleted)
}

func TestHandleRestartRequest_SuccessCleansUp(t *testing.T) {
	repo := &mockRestarterRepository{}
	api := &fakeGithubAPI{
		runJSON: runJSON("completed", "success", "feature"),
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
