package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- Mock tray repository ---

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

// --- Mock provider ---

type mockProvider struct {
	name     string
	runErr   error
	cleanErr error
	runCalls int
	cleaned  []string
}

func (m *mockProvider) GetProviderName() string { return m.name }
func (m *mockProvider) RunTray(_ *trays.Tray) error {
	m.runCalls++
	return m.runErr
}
func (m *mockProvider) CleanTray(tray *trays.Tray) error {
	m.cleaned = append(m.cleaned, tray.Id)
	return m.cleanErr
}

// --- Mock provider factory ---

type mockProviderFactory struct {
	provider   *mockProvider
	getErr     error
	forTrayErr error
}

func (m *mockProviderFactory) GetProvider(_ string) (providers.ITrayProvider, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.provider, nil
}

func (m *mockProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.ITrayProvider, error) {
	if m.forTrayErr != nil {
		return nil, m.forTrayErr
	}
	return m.provider, nil
}

// --- Helper ---

func newTestManager(repo *mockTrayRepository, pf *mockProviderFactory) *TrayManager {
	return NewTrayManager(repo, pf)
}

// --- Tests ---

func TestLogCreationResults_AllSuccess(t *testing.T) {
	tm := newTestManager(newMockRepo(), &mockProviderFactory{})
	results := []error{nil, nil, nil}

	err := tm.logCreationResults("test-type", results)
	assert.NoError(t, err)
}

func TestLogCreationResults_AllFailed(t *testing.T) {
	tm := newTestManager(newMockRepo(), &mockProviderFactory{})
	results := []error{
		errors.New("fail1"),
		errors.New("fail2"),
	}

	err := tm.logCreationResults("test-type", results)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all 2 tray creations failed")
}

func TestLogCreationResults_PartialFailure(t *testing.T) {
	tm := newTestManager(newMockRepo(), &mockProviderFactory{})
	results := []error{nil, errors.New("fail"), nil}

	err := tm.logCreationResults("test-type", results)
	assert.NoError(t, err)
}

func TestScaleForDemand_NoScaleNeeded(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 5
	tm := newTestManager(repo, &mockProviderFactory{})

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
	tm := newTestManager(repo, &mockProviderFactory{})

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 5)
	assert.Error(t, err)
}

func TestScaleForDemand_ScalesUp(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 2
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:     "test-type",
		Provider: "docker",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 5)
	assert.NoError(t, err)
	assert.Equal(t, 3, prov.runCalls)
}

func TestScaleForDemand_CappedByMaxTrays(t *testing.T) {
	repo := newMockRepo()
	repo.countResult = 8
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:     "test-type",
		Provider: "docker",
		MaxTrays: 10,
	}

	// desired=20, active=8, max=10 → creates min(20-8, 10-8)=2
	err := tm.ScaleForDemand(context.Background(), trayType, 20)
	assert.NoError(t, err)
	assert.Equal(t, 2, prov.runCalls)
}

func TestCreateTray_Success(t *testing.T) {
	repo := newMockRepo()
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.NoError(t, err)
	assert.Equal(t, 1, prov.runCalls)
	assert.Equal(t, 1, len(repo.trays))
}

func TestCreateTray_ProviderError(t *testing.T) {
	repo := newMockRepo()
	prov := &mockProvider{name: "docker", runErr: errors.New("docker failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	assert.Equal(t, 0, len(repo.trays))
}

func TestCreateTray_SaveError_CleansUp(t *testing.T) {
	repo := newMockRepo()
	repo.saveErr = errors.New("db error")
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	// Provider should have been called to clean up after save failure
	assert.Equal(t, 1, len(prov.cleaned))
}

func TestCreateTray_FactoryError(t *testing.T) {
	repo := newMockRepo()
	factory := &mockProviderFactory{getErr: errors.New("no provider")}
	tm := newTestManager(repo, factory)

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider")
}

func TestDeleteTray_Success(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type", ProviderName: "docker"}
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, 1, len(prov.cleaned))
	assert.Equal(t, 0, len(repo.trays))
}

func TestDeleteTray_NotFound(t *testing.T) {
	repo := newMockRepo()
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, tray)
	assert.Equal(t, 0, len(prov.cleaned))
}

func TestDeleteTray_ProviderCleanError(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	prov := &mockProvider{name: "docker", cleanErr: errors.New("clean failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
	// Tray should still be in repo (not deleted) since provider clean failed
	assert.Equal(t, 1, len(repo.trays))
}

func TestDeleteTray_FactoryError(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	factory := &mockProviderFactory{forTrayErr: errors.New("no provider")}
	tm := newTestManager(repo, factory)

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestGetTrayById_Found(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test"}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, "tray-1", tray.Id)
}

func TestGetTrayById_NotFound(t *testing.T) {
	repo := newMockRepo()
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, tray)
}

func TestGetTrayById_Error(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db error")
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistering(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusCreating}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registering(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistering, tray.Status)
}

func TestRegistering_NotFound(t *testing.T) {
	repo := newMockRepo()
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registering(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistered(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistering}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registered(context.Background(), "tray-1", 42)
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistered, tray.Status)
	assert.Equal(t, int64(42), tray.GitHubRunnerId)
}

func TestSetJob(t *testing.T) {
	repo := newMockRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistered}
	tm := newTestManager(repo, &mockProviderFactory{})

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
	tm := newTestManager(repo, &mockProviderFactory{})

	count, err := tm.CountTrays(context.Background(), "test-type")
	assert.NoError(t, err)
	assert.Equal(t, 7, count)
}
