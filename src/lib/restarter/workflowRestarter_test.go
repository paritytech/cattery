package restarter

import (
	"cattery/lib/restarter/repositories"
	"context"
	"errors"
	"testing"
	"time"

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

func TestNewWorkflowRestarter(t *testing.T) {
	repo := &mockRestarterRepository{}
	wr := NewWorkflowRestarter(repo)
	assert.NotNil(t, wr)
}
