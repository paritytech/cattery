package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/scaleSetClient"
	"cattery/lib/testutil"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- Mock provider ---

type mockProvider struct {
	mu         sync.Mutex
	name       string
	startErr   error
	waitErr    error
	cleanErr   error
	startCalls int
	waitCalls  int
	cleaned    []string
	// onStart, if set, is called inside StartDeploy so tests can mutate state
	// (e.g. simulate a concurrent unregister) before the manager persists
	// provider data.
	onStart func(*trays.Tray)
	// onWait, if set, is called inside WaitDeploy. Useful for asserting on
	// state observed between StartDeploy and WaitDeploy from the manager's
	// point of view.
	onWait func(*trays.Tray)
}

func (m *mockProvider) GetProviderName() string { return m.name }
func (m *mockProvider) StartDeploy(_ context.Context, tray *trays.Tray) error {
	m.mu.Lock()
	m.startCalls++
	cb := m.onStart
	m.mu.Unlock()
	if cb != nil {
		cb(tray)
	}
	return m.startErr
}
func (m *mockProvider) WaitDeploy(_ context.Context, tray *trays.Tray) error {
	m.mu.Lock()
	m.waitCalls++
	cb := m.onWait
	err := m.waitErr
	m.mu.Unlock()
	if cb != nil {
		cb(tray)
	}
	return err
}
func (m *mockProvider) CleanTray(_ context.Context, tray *trays.Tray) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleaned = append(m.cleaned, tray.Id)
	return m.cleanErr
}

// --- Mock provider factory ---

type mockProviderFactory struct {
	provider   *mockProvider
	getErr     error
	forTrayErr error
}

func (m *mockProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.provider, nil
}

func (m *mockProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	if m.forTrayErr != nil {
		return nil, m.forTrayErr
	}
	return m.provider, nil
}

// --- Helper ---

func newTestManager(repo *testutil.MockTrayRepository, pf *mockProviderFactory) *TrayManager {
	return NewTrayManager(repo, pf, nil)
}

// --- Mock runner remover ---

// mockRunnerRemover answers every RemoveRunner call with err and records the
// runner IDs it was asked about.
type mockRunnerRemover struct {
	mu    sync.Mutex
	err   error
	calls []int64
	// onRemove, if set, is called inside RemoveRunner so tests can observe the
	// state the manager had reached when it deregistered the runner.
	onRemove func()
}

func (m *mockRunnerRemover) RemoveRunner(_ context.Context, _ string, runnerId int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, runnerId)
	if m.onRemove != nil {
		m.onRemove()
	}
	return m.err
}

func (m *mockRunnerRemover) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// --- Tests ---

func TestLogCreationResults_AllSuccess(t *testing.T) {
	tm := newTestManager(testutil.NewMockTrayRepository(), &mockProviderFactory{})
	results := []error{nil, nil, nil}

	err := tm.logCreationResults("test-type", results)
	assert.NoError(t, err)
}

func TestLogCreationResults_AllFailed(t *testing.T) {
	tm := newTestManager(testutil.NewMockTrayRepository(), &mockProviderFactory{})
	results := []error{
		errors.New("fail1"),
		errors.New("fail2"),
	}

	err := tm.logCreationResults("test-type", results)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all 2 tray creations failed")
}

func TestLogCreationResults_PartialFailure(t *testing.T) {
	tm := newTestManager(testutil.NewMockTrayRepository(), &mockProviderFactory{})
	results := []error{nil, errors.New("fail"), nil}

	err := tm.logCreationResults("test-type", results)
	assert.NoError(t, err)
}

func TestScaleForDemand_NoScaleNeeded(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.CountResult = 5
	tm := newTestManager(repo, &mockProviderFactory{})

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 3)
	assert.NoError(t, err)
}

func TestScaleForDemand_CountError(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.CountErr = errors.New("db error")
	tm := newTestManager(repo, &mockProviderFactory{})

	trayType := &config.TrayType{
		Name:     "test-type",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 5)
	assert.Error(t, err)
}

func TestScaleForDemand_ScalesUp(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.CountResult = 2
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:     "test-type",
		Provider: "docker",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 5)
	assert.NoError(t, err)
	assert.Equal(t, 3, prov.startCalls)
}

func TestScaleForDemand_CappedByMaxTrays(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.CountResult = 8
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:     "test-type",
		Provider: "docker",
		MaxTrays: 10,
	}

	err := tm.ScaleForDemand(context.Background(), trayType, 20)
	assert.NoError(t, err)
	assert.Equal(t, 2, prov.startCalls)
}

func TestCreateTray_Success(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.NoError(t, err)
	assert.Equal(t, 1, prov.startCalls)
	assert.Equal(t, 1, prov.waitCalls)
	assert.Equal(t, 1, len(repo.Trays))
}

func TestCreateTray_RowExistsBeforeProviderCall(t *testing.T) {
	// The whole point of save-before-deploy: when StartDeploy runs, the row
	// must already be in the repository, so an agent registering against a
	// fast-booting VM finds something.
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{
		name: "docker",
		onStart: func(tray *trays.Tray) {
			// Simulate the agent's registration handler running while StartDeploy
			// is still in flight. It must see the row.
			got, _ := repo.GetById(context.Background(), tray.Id)
			assert.NotNil(t, got, "tray row must exist before StartDeploy completes")
		},
	}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{Name: "test-type", Provider: "docker", GitHubOrg: "test-org"}
	err := tm.CreateTray(context.Background(), trayType)
	assert.NoError(t, err)
}

func TestCreateTray_StartDeployError_CleansUp(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker", startErr: errors.New("docker failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	// StartDeploy failure triggers cleanup; on successful clean the row is
	// deleted. WaitDeploy should not run after a StartDeploy error.
	assert.Equal(t, 0, prov.waitCalls)
	assert.Equal(t, 1, len(prov.cleaned))
	assert.Equal(t, 0, len(repo.Trays))
}

func TestCreateTray_StartDeployError_CleanupFails_RowStays(t *testing.T) {
	// When the deploy fails AND cleanup also fails, the row must remain in
	// "deleting" status so the stale handler retries it.
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{
		name:     "docker",
		startErr: errors.New("docker failed"),
		cleanErr: errors.New("clean also failed"),
	}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	assert.Equal(t, 1, len(repo.Trays))
	for _, tr := range repo.Trays {
		assert.Equal(t, trays.TrayStatusDeleting, tr.Status, "row should be left in deleting state for the stale handler")
	}
}

func TestCreateTray_ConcurrentDelete_TriggersCleanup(t *testing.T) {
	// If an unregister arrives while a deploy is running, the persist that
	// follows WaitDeploy observes status=deleting and cleanup runs without
	// waiting for the stale handler. WaitDeploy itself is still allowed to
	// run — we accept that wasted wait in exchange for a simpler flow.
	repo := testutil.NewMockTrayRepository()
	var trayId string
	prov := &mockProvider{name: "docker"}
	prov.onStart = func(tray *trays.Tray) {
		trayId = tray.Id
		// Simulate a concurrent unregister flipping status to deleting.
		_, _ = repo.UpdateStatus(context.Background(), tray.Id, trays.TrayStatusDeleting, 0, 0, 0, "", "", "")
	}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{Name: "test-type", Provider: "docker", GitHubOrg: "test-org"}
	err := tm.CreateTray(context.Background(), trayType)

	assert.NoError(t, err, "concurrent delete handled inline is not an error to the caller")
	assert.NotEmpty(t, trayId)
	assert.Equal(t, 1, len(prov.cleaned))
	assert.Equal(t, 0, len(repo.Trays))
}

func TestCreateTray_WaitDeployError_CleansUp(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker", waitErr: errors.New("vm never ready")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{Name: "test-type", Provider: "docker", GitHubOrg: "test-org"}
	err := tm.CreateTray(context.Background(), trayType)

	assert.Error(t, err)
	assert.Equal(t, 1, prov.startCalls)
	assert.Equal(t, 1, prov.waitCalls)
	assert.Equal(t, 1, len(prov.cleaned))
	assert.Equal(t, 0, len(repo.Trays))
}

func TestCreateTray_WaitDeployError_CleanupFails_RowStays(t *testing.T) {
	// Symmetric to TestCreateTray_StartDeployError_CleanupFails_RowStays:
	// a wait-phase failure with cleanup also failing must leave the row in
	// "deleting" state for the stale handler to retry.
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{
		name:     "docker",
		waitErr:  errors.New("vm never ready"),
		cleanErr: errors.New("clean also failed"),
	}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{Name: "test-type", Provider: "docker", GitHubOrg: "test-org"}
	err := tm.CreateTray(context.Background(), trayType)

	assert.Error(t, err)
	assert.Equal(t, 1, len(repo.Trays))
	for _, tr := range repo.Trays {
		assert.Equal(t, trays.TrayStatusDeleting, tr.Status)
	}
}

func TestCreateTray_StartDeployData_PersistedBeforeWait(t *testing.T) {
	// Crash-safety guarantee: anything StartDeploy puts in tray.ProviderData
	// must be persisted to the repository before WaitDeploy starts blocking.
	// Otherwise a server crash mid-wait leaves a real VM running with no
	// cleanup handle in the database.
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker"}
	prov.onStart = func(tray *trays.Tray) {
		tray.ProviderData["zone"] = "us-east-1"
		tray.ProviderData["instanceId"] = "i-12345"
	}
	prov.onWait = func(tray *trays.Tray) {
		// By the time WaitDeploy runs, the provider data populated in
		// StartDeploy must be visible in the repository.
		got, _ := repo.GetById(context.Background(), tray.Id)
		assert.NotNil(t, got)
		assert.Equal(t, "us-east-1", got.ProviderData["zone"])
		assert.Equal(t, "i-12345", got.ProviderData["instanceId"])
	}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{Name: "test-type", Provider: "docker", GitHubOrg: "test-org"}
	err := tm.CreateTray(context.Background(), trayType)
	assert.NoError(t, err)
}

func TestCreateTray_InitialSaveError(t *testing.T) {
	// With save-before-deploy, an initial Save failure means no provider
	// resource was ever requested — nothing to clean up.
	repo := testutil.NewMockTrayRepository()
	repo.SaveErr = errors.New("db error")
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	assert.Equal(t, 0, prov.startCalls)
	assert.Equal(t, 0, len(prov.cleaned))
}

func TestCreateTray_FactoryError(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
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
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type", ProviderName: "docker"}
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, 1, len(prov.cleaned))
	assert.Equal(t, 0, len(repo.Trays))
}

func TestDeleteTray_NotFound(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, tray)
	assert.Equal(t, 0, len(prov.cleaned))
}

func TestDeleteTray_ProviderCleanError(t *testing.T) {
	// Cleanup failure is logged and swallowed: the row stays in "deleting"
	// state so the stale handler can retry, and the caller (typically the
	// unregister HTTP handler) gets a soft success.
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	prov := &mockProvider{name: "docker", cleanErr: errors.New("clean failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, 1, len(repo.Trays))
	assert.Equal(t, trays.TrayStatusDeleting, repo.Trays["tray-1"].Status)
}

func TestDeleteTray_RepoDeleteFails_Propagates(t *testing.T) {
	// Cleanup succeeded, but the repository's Delete call failed. This is a
	// genuine repo-level failure (DB blip) and must propagate to the caller
	// so the upstream layer can react. The row remains in deleting status —
	// the stale handler will try again next tick.
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	repo.DeleteErr = errors.New("db transient error")
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.NotNil(t, tray, "tray returned even when row delete fails so callers can log the id")
	assert.Equal(t, 1, len(prov.cleaned), "cleanup ran successfully")
	assert.Equal(t, 1, len(repo.Trays), "row remains for stale handler retry")
}

func TestDeleteTray_UpdateStatusFails_Propagates(t *testing.T) {
	// If we can't even mark the row as deleting, propagate immediately —
	// there's no way to make progress. CleanTray should not run.
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	repo.UpdateErr = errors.New("db error")
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
	assert.Equal(t, 0, len(prov.cleaned), "must not attempt cleanup if status couldn't be marked")
}

func TestDeleteTray_FactoryError(t *testing.T) {
	// Provider config missing for this tray's type — log it, leave the row
	// in deleting for the stale handler to keep trying. Don't fail the
	// caller; an unregister request shouldn't return 500 to an agent that's
	// already shutting down.
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	factory := &mockProviderFactory{forTrayErr: errors.New("no provider")}
	tm := newTestManager(repo, factory)

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, 1, len(repo.Trays))
	assert.Equal(t, trays.TrayStatusDeleting, repo.Trays["tray-1"].Status)
}

func TestGetTrayById_Found(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test"}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.Equal(t, "tray-1", tray.Id)
}

func TestGetTrayById_NotFound(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, tray)
}

func TestGetTrayById_Error(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.GetErr = errors.New("db error")
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.GetTrayById(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistering(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusCreating}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registering(context.Background(), "tray-1")
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistering, tray.Status)
}

func TestRegistering_NotFound(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registering(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, tray)
}

func TestRegistered(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistering}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.Registered(context.Background(), "tray-1", 42)
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistered, tray.Status)
	assert.Equal(t, int64(42), tray.GitHubRunnerId)
}

func TestSetJob(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRegistered}
	tm := newTestManager(repo, &mockProviderFactory{})

	tray, err := tm.SetJob(context.Background(), "tray-1", 100, 200, "org/repo", "build", "ci")
	assert.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRunning, tray.Status)
	assert.Equal(t, int64(100), tray.JobRunId)
	assert.Equal(t, int64(200), tray.WorkflowRunId)
	assert.Equal(t, "org/repo", tray.Repository)
}

func TestCountTrays(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.CountResult = 7
	tm := newTestManager(repo, &mockProviderFactory{})

	count, err := tm.CountTrays(context.Background(), "test-type")
	assert.NoError(t, err)
	assert.Equal(t, 7, count)
}

func TestResolveStaleThresholds(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]time.Duration
		want map[trays.TrayStatus]time.Duration
	}{
		{
			name: "valid statuses pass through",
			in: map[string]time.Duration{
				"creating":    5 * time.Minute,
				"registering": 5 * time.Minute,
				"registered":  15 * time.Minute,
				"deleting":    10 * time.Minute,
			},
			want: map[trays.TrayStatus]time.Duration{
				trays.TrayStatusCreating:    5 * time.Minute,
				trays.TrayStatusRegistering: 5 * time.Minute,
				trays.TrayStatusRegistered:  15 * time.Minute,
				trays.TrayStatusDeleting:    10 * time.Minute,
			},
		},
		{
			name: "case-insensitive status names",
			in: map[string]time.Duration{
				"Creating":   1 * time.Minute,
				"REGISTERED": 2 * time.Minute,
			},
			want: map[trays.TrayStatus]time.Duration{
				trays.TrayStatusCreating:   1 * time.Minute,
				trays.TrayStatusRegistered: 2 * time.Minute,
			},
		},
		{
			name: "unknown status is skipped",
			in: map[string]time.Duration{
				"creating": 5 * time.Minute,
				"bogus":    1 * time.Minute,
			},
			want: map[trays.TrayStatus]time.Duration{
				trays.TrayStatusCreating: 5 * time.Minute,
			},
		},
		{
			name: "running is rejected (running trays are never stale)",
			in: map[string]time.Duration{
				"creating": 5 * time.Minute,
				"running":  1 * time.Minute,
			},
			want: map[trays.TrayStatus]time.Duration{
				trays.TrayStatusCreating: 5 * time.Minute,
			},
		},
		{
			name: "non-positive duration is rejected",
			in: map[string]time.Duration{
				"creating":    5 * time.Minute,
				"registering": 0,
				"registered":  -1 * time.Minute,
			},
			want: map[trays.TrayStatus]time.Duration{
				trays.TrayStatusCreating: 5 * time.Minute,
			},
		},
		{
			name: "empty input yields empty output",
			in:   map[string]time.Duration{},
			want: map[trays.TrayStatus]time.Duration{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStaleThresholds(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHandleStale_DeletesStaleTrays(t *testing.T) {
	// Override config so the loop polls fast.
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{
			ListenAddress: ":8080",
			AdvertiseUrl:  "http://localhost",
		},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"creating": time.Second},
		},
	})

	repo := testutil.NewMockTrayRepository()
	repo.SetStale([]*trays.Tray{
		{Id: "stale-1", TrayTypeName: "t1", ProviderName: "docker", Status: trays.TrayStatusCreating},
		{Id: "stale-2", TrayTypeName: "t1", ProviderName: "docker", Status: trays.TrayStatusCreating},
	}, nil)

	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.HandleStale(ctx)

	// Expect both stale trays to be cleaned within a few poll cycles.
	assert.Eventually(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		seen := map[string]bool{}
		for _, id := range prov.cleaned {
			seen[id] = true
		}
		return seen["stale-1"] && seen["stale-2"]
	}, 500*time.Millisecond, 10*time.Millisecond, "expected both stale trays to be cleaned")
}

func TestHandleStale_GetStaleErrorDoesNotCrashLoop(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server:   config.ServerConfig{ListenAddress: ":8080", AdvertiseUrl: "http://localhost"},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"creating": time.Second},
		},
	})

	repo := testutil.NewMockTrayRepository()
	repo.SetStale(nil, errors.New("transient db error"))

	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	ctx, cancel := context.WithCancel(context.Background())
	go tm.HandleStale(ctx)

	// Let it tick a few times under error condition.
	time.Sleep(50 * time.Millisecond)

	// Recover: clear error, populate a stale tray.
	tray := &trays.Tray{Id: "recovered", ProviderName: "docker", Status: trays.TrayStatusCreating}
	repo.SetStale([]*trays.Tray{tray}, nil)

	assert.Eventually(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		for _, id := range prov.cleaned {
			if id == "recovered" {
				return true
			}
		}
		return false
	}, 500*time.Millisecond, 10*time.Millisecond, "loop did not recover after transient error")

	cancel()
}

func TestHandleStale_ContextCancellationStopsLoop(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server:   config.ServerConfig{ListenAddress: ":8080", AdvertiseUrl: "http://localhost"},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"creating": time.Second},
		},
	})

	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	ctx, cancel := context.WithCancel(context.Background())
	go tm.HandleStale(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()

	// After cancel, no further GetStale-driven activity should occur.
	// Capture provider.cleaned snapshot, wait, verify unchanged.
	prov.mu.Lock()
	before := len(prov.cleaned)
	prov.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	prov.mu.Lock()
	after := len(prov.cleaned)
	prov.mu.Unlock()

	assert.Equal(t, before, after, "loop kept running after context cancellation")
}

// --- Runner deregistration on tray removal ---

// Every removal starts on GitHub: the runner is deregistered while the tray is
// still untouched, and only then is the tray marked as deleting and cleaned up.
func TestDeleteTray_DeregistersRunnerBeforeTouchingTheTray(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{
		Id: "tray-1", TrayTypeName: "t1", ProviderName: "docker", GitHubRunnerId: 99, Status: trays.TrayStatusRunning,
	}
	prov := &mockProvider{name: "docker"}

	var statusAtRemoval trays.TrayStatus
	var cleanedAtRemoval int
	remover := &mockRunnerRemover{}
	remover.onRemove = func() {
		statusAtRemoval = repo.Trays["tray-1"].Status
		prov.mu.Lock()
		cleanedAtRemoval = len(prov.cleaned)
		prov.mu.Unlock()
	}
	tm := NewTrayManager(repo, &mockProviderFactory{provider: prov}, remover)

	_, err := tm.DeleteTray(context.Background(), "tray-1")

	assert.NoError(t, err)
	assert.Equal(t, []int64{99}, remover.calls)
	assert.Equal(t, trays.TrayStatusRunning, statusAtRemoval, "the runner must be deregistered before the tray is marked as deleting")
	assert.Zero(t, cleanedAtRemoval, "the runner must be deregistered before the upstream resource is cleaned up")
	assert.Equal(t, []string{"tray-1"}, prov.cleaned)
	assert.Empty(t, repo.Trays)
}

func TestDeleteTray_RunnerDeregistration(t *testing.T) {
	busy := scaleSetClient.ErrRunnerBusy
	unreachable := errors.New("context deadline exceeded")

	tests := []struct {
		name        string
		status      trays.TrayStatus
		runnerId    int64
		removeErr   error
		wantDeleted bool
		wantAsked   bool
	}{
		// A registered tray is only assumed to be idle, so it goes only once
		// its runner is gone.
		{name: "registered: runner deregistered", status: trays.TrayStatusRegistered, runnerId: 99, wantDeleted: true, wantAsked: true},
		{name: "registered: runner busy", status: trays.TrayStatusRegistered, runnerId: 99, removeErr: busy, wantAsked: true},
		{name: "registered: wrapped busy error", status: trays.TrayStatusRegistered, runnerId: 99, removeErr: fmt.Errorf("remove: %w", busy), wantAsked: true},
		{name: "registered: github unreachable", status: trays.TrayStatusRegistered, runnerId: 99, removeErr: unreachable, wantAsked: true},
		{name: "registered: tray type has no client", status: trays.TrayStatusRegistered, runnerId: 99, removeErr: scaleSetClient.ErrNoClient, wantDeleted: true, wantAsked: true},

		// A running tray is removed because its job completed or its agent is
		// leaving, so GitHub's answer never stops the removal.
		{name: "running: runner deregistered", status: trays.TrayStatusRunning, runnerId: 99, wantDeleted: true, wantAsked: true},
		{name: "running: runner still busy (agent preempted mid-job)", status: trays.TrayStatusRunning, runnerId: 99, removeErr: busy, wantDeleted: true, wantAsked: true},
		{name: "running: github unreachable", status: trays.TrayStatusRunning, runnerId: 99, removeErr: unreachable, wantDeleted: true, wantAsked: true},

		// A deleting tray was given up on earlier. Its cleanup retry must not
		// depend on GitHub.
		{name: "deleting: runner busy", status: trays.TrayStatusDeleting, runnerId: 99, removeErr: busy, wantDeleted: true, wantAsked: true},
		{name: "deleting: github unreachable", status: trays.TrayStatusDeleting, runnerId: 99, removeErr: unreachable, wantDeleted: true, wantAsked: true},

		// No runner was registered yet, so there is nothing to ask GitHub.
		{name: "creating: no runner", status: trays.TrayStatusCreating, removeErr: busy, wantDeleted: true},
		{name: "registering: no runner", status: trays.TrayStatusRegistering, removeErr: busy, wantDeleted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := testutil.NewMockTrayRepository()
			repo.Trays["tray-1"] = &trays.Tray{
				Id: "tray-1", TrayTypeName: "t1", ProviderName: "docker", GitHubRunnerId: tc.runnerId, Status: tc.status,
			}
			prov := &mockProvider{name: "docker"}
			remover := &mockRunnerRemover{err: tc.removeErr}
			tm := NewTrayManager(repo, &mockProviderFactory{provider: prov}, remover)

			_, err := tm.DeleteTray(context.Background(), "tray-1")

			if tc.wantDeleted {
				assert.NoError(t, err)
				assert.Equal(t, []string{"tray-1"}, prov.cleaned)
				assert.Empty(t, repo.Trays)
			} else {
				assert.ErrorIs(t, err, tc.removeErr)
				assert.Empty(t, prov.cleaned, "a kept tray must not be cleaned up")
				if assert.Contains(t, repo.Trays, "tray-1") {
					assert.Equal(t, tc.status, repo.Trays["tray-1"].Status, "a kept tray must be left untouched")
				}
			}

			if tc.wantAsked {
				assert.Equal(t, []int64{99}, remover.calls)
			} else {
				assert.Empty(t, remover.calls, "there is no runner to deregister")
			}
		})
	}
}

func TestDeleteTray_NoRemoverConfigured(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", ProviderName: "docker", GitHubRunnerId: 99, Status: trays.TrayStatusRegistered}
	prov := &mockProvider{name: "docker"}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	_, err := tm.DeleteTray(context.Background(), "tray-1")

	assert.NoError(t, err)
	assert.Empty(t, repo.Trays)
}

// staleTestConfig makes HandleStale poll fast. The threshold itself is never
// evaluated in these tests: the mock repository returns whatever SetStale was
// given.
func staleTestConfig(t *testing.T) {
	t.Helper()
	config.SetForTest(t, &config.CatteryConfig{
		Server:   config.ServerConfig{ListenAddress: ":8080", AdvertiseUrl: "http://localhost"},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"registered": 15 * time.Minute, "deleting": 15 * time.Minute},
		},
	})
}

// The failure this guards against: the listener never delivered JobStarted, so
// a tray that is running a job still reads "registered" when it crosses the
// stale threshold. GitHub refuses to deregister its runner, and the VM must
// survive.
func TestHandleStale_KeepsRegisteredTrayWhoseRunnerIsBusy(t *testing.T) {
	staleTestConfig(t)

	repo := testutil.NewMockTrayRepository()
	repo.SetStale([]*trays.Tray{
		{Id: "busy-1", TrayTypeName: "t1", ProviderName: "docker", GitHubRunnerId: 99, Status: trays.TrayStatusRegistered},
	}, nil)

	prov := &mockProvider{name: "docker"}
	remover := &mockRunnerRemover{err: scaleSetClient.ErrRunnerBusy}
	tm := NewTrayManager(repo, &mockProviderFactory{provider: prov}, remover)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tm.HandleStale(ctx)

	assert.Never(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		return len(prov.cleaned) > 0
	}, 200*time.Millisecond, 10*time.Millisecond, "a tray whose runner is executing a job must not be destroyed")

	assert.Greater(t, remover.callCount(), 1, "the tray should be re-checked on every pass")

	tray, err := repo.GetById(context.Background(), "busy-1")
	assert.NoError(t, err)
	if assert.NotNil(t, tray, "a kept tray must stay in the repository") {
		assert.Equal(t, trays.TrayStatusRegistered, tray.Status, "a kept tray must not be marked as deleting")
	}
}

func TestHandleStale_DeletesRegisteredTrayOnceRunnerIsDeregistered(t *testing.T) {
	staleTestConfig(t)

	repo := testutil.NewMockTrayRepository()
	repo.SetStale([]*trays.Tray{
		{Id: "idle-1", TrayTypeName: "t1", ProviderName: "docker", GitHubRunnerId: 99, Status: trays.TrayStatusRegistered},
	}, nil)

	prov := &mockProvider{name: "docker"}
	remover := &mockRunnerRemover{}
	tm := NewTrayManager(repo, &mockProviderFactory{provider: prov}, remover)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tm.HandleStale(ctx)

	assert.Eventually(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		return len(prov.cleaned) > 0
	}, 500*time.Millisecond, 10*time.Millisecond, "an idle tray must still be reaped")

	assert.GreaterOrEqual(t, remover.callCount(), 1, "the runner must be deregistered before the tray is destroyed")
}

// A tray already in "deleting" is waiting for a cleanup retry. That retry must
// not depend on GitHub: the decision to destroy the tray was made earlier.
func TestHandleStale_RetriesDeletingTrayEvenWhenGitHubFails(t *testing.T) {
	staleTestConfig(t)

	repo := testutil.NewMockTrayRepository()
	repo.SetStale([]*trays.Tray{
		{Id: "deleting-1", TrayTypeName: "t1", ProviderName: "docker", GitHubRunnerId: 99, Status: trays.TrayStatusDeleting},
	}, nil)

	prov := &mockProvider{name: "docker"}
	remover := &mockRunnerRemover{err: errors.New("github is down")}
	tm := NewTrayManager(repo, &mockProviderFactory{provider: prov}, remover)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tm.HandleStale(ctx)

	assert.Eventually(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		return len(prov.cleaned) > 0
	}, 500*time.Millisecond, 10*time.Millisecond, "cleanup of a deleting tray must be retried")
}

func TestStaleConfigWithDefaults(t *testing.T) {
	t.Run("zero values populate from defaults", func(t *testing.T) {
		out := config.StaleConfig{}.WithDefaults()
		assert.Equal(t, config.DefaultStalePollInterval, out.PollInterval)
		assert.Equal(t, config.DefaultStaleThresholds, out.Thresholds)
	})

	t.Run("set values are preserved", func(t *testing.T) {
		in := config.StaleConfig{
			PollInterval: 30 * time.Second,
			Thresholds: map[string]time.Duration{
				"creating": 2 * time.Minute,
			},
		}
		out := in.WithDefaults()
		assert.Equal(t, 30*time.Second, out.PollInterval)
		assert.Equal(t, map[string]time.Duration{"creating": 2 * time.Minute}, out.Thresholds)
	})

	t.Run("partial: only PollInterval set", func(t *testing.T) {
		in := config.StaleConfig{PollInterval: 30 * time.Second}
		out := in.WithDefaults()
		assert.Equal(t, 30*time.Second, out.PollInterval)
		assert.Equal(t, config.DefaultStaleThresholds, out.Thresholds)
	})
}
