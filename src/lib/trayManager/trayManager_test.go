package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockTrayRepository implements repositories.ITrayRepository for testing
type mockTrayRepository struct {
	trays       map[string]*trays.Tray
	countResult int
	countErr    error
	saveErr     error
	updateErr   error
	deleteErr   error
	getErr      error
	staleTrays  []*trays.Tray
	staleErr    error
}

func newMockRepo() *mockTrayRepository {
	return &mockTrayRepository{
		trays: make(map[string]*trays.Tray),
	}
}

func (m *mockTrayRepository) GetById(_ context.Context, trayId string) (*trays.Tray, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.trays[trayId], nil
}

func (m *mockTrayRepository) Save(_ context.Context, tray *trays.Tray) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.trays[tray.Id] = tray
	return nil
}

func (m *mockTrayRepository) Delete(_ context.Context, trayId string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.trays, trayId)
	return nil
}

func (m *mockTrayRepository) UpdateStatus(_ context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string) (*trays.Tray, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	tray, ok := m.trays[trayId]
	if !ok {
		return nil, nil
	}
	tray.Status = status
	if ghRunnerId != 0 {
		tray.GitHubRunnerId = ghRunnerId
	}
	if jobRunId != 0 {
		tray.JobRunId = jobRunId
	}
	if workflowRunId != 0 {
		tray.WorkflowRunId = workflowRunId
	}
	if repository != "" {
		tray.Repository = repository
	}
	tray.StatusChanged = time.Now()
	return tray, nil
}

func (m *mockTrayRepository) CountActive(_ context.Context, _ string) (int, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.countResult, nil
}

func (m *mockTrayRepository) GetStale(_ context.Context, _ time.Duration) ([]*trays.Tray, error) {
	if m.staleErr != nil {
		return nil, m.staleErr
	}
	return m.staleTrays, nil
}

// --- Tests ---

func TestLogCreationResults_AllSuccess(t *testing.T) {
	tm := NewTrayManager(newMockRepo())
	results := []error{nil, nil, nil}

	err := tm.logCreationResults("test-type", results)
	assert.NoError(t, err)
}

func TestLogCreationResults_AllFailed(t *testing.T) {
	tm := NewTrayManager(newMockRepo())
	results := []error{
		errors.New("fail1"),
		errors.New("fail2"),
	}

	err := tm.logCreationResults("test-type", results)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all 2 tray creations failed")
}

func TestLogCreationResults_PartialFailure(t *testing.T) {
	tm := NewTrayManager(newMockRepo())
	results := []error{nil, errors.New("fail"), nil}

	err := tm.logCreationResults("test-type", results)
	// Partial failure logs a warning but does not return an error
	assert.NoError(t, err)
}

func TestScaleForDemand_NoScaleNeeded(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 5
	tm := NewTrayManager(repo)

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 3)
	assert.NoError(t, err)
}

func TestScaleForDemand_CountError(t *testing.T) {
	repo := newMockRepo()
	repo.countErr = errors.New("db error")
	tm := NewTrayManager(repo)

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 5)
	assert.Error(t, err)
}

func TestScaleForDemand_CappedByMaxTrays(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 8
	tm := NewTrayManager(repo)

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	// desired=20, active=8, max=10 → would create min(20-8, 10-8)=2
	// But CreateTray needs a provider, so this will error.
	// We're testing that ScaleForDemand attempts creation (doesn't short-circuit).
	err := tm.ScaleForDemand(context.Background(), trayType, 20)
	// Will error because no provider is configured, but that's expected —
	// the important thing is it didn't return nil (i.e., it tried to scale)
	assert.Error(t, err)
}

func TestGetTrayById_Found(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test"}
	tm := NewTrayManager(repo)

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, "tray-1", tray.Id)
}

func TestGetTrayById_NotFound(t *testing.T) {
	repo := newMockRepo()
	tm := NewTrayManager(repo)

	tray, err := tm.GetTrayById(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, tray)
}

func TestGetTrayById_Error(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db error")
	tm := NewTrayManager(repo)

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistering(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusCreating}
	tm := NewTrayManager(repo)

	tray, err := tm.Registering(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistering, tray.Status)
}

func TestRegistering_NotFound(t *testing.T) {
	repo := newMockRepo()
	tm := NewTrayManager(repo)

	tray, err := tm.Registering(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistered(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistering}
	tm := NewTrayManager(repo)

	tray, err := tm.Registered(context.Background(), "tray-1", 42)
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistered, tray.Status)
	assert.Equal(t, int64(42), tray.GitHubRunnerId)
}

func TestSetJob(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistered}
	tm := NewTrayManager(repo)

	tray, err := tm.SetJob(context.Background(), "tray-1", 100, 200, "org/repo")
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRunning, tray.Status)
	assert.Equal(t, int64(100), tray.JobRunId)
	assert.Equal(t, int64(200), tray.WorkflowRunId)
	assert.Equal(t, "org/repo", tray.Repository)
}

func TestCountTrays(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 7
	tm := NewTrayManager(repo)

	count, err := tm.CountTrays(context.Background(), "test-type")
	assert.NoError(t, err)
	assert.Equal(t, 7, count)
}
