package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/testutil"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- Mock provider ---

type mockProvider struct {
	mu       sync.Mutex
	name     string
	runErr   error
	cleanErr error
	runCalls int
	cleaned  []string
}

func (m *mockProvider) GetProviderName() string { return m.name }
func (m *mockProvider) RunTray(_ context.Context, _ *trays.Tray) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runCalls++
	return m.runErr
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
	return NewTrayManager(repo, pf)
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
	assert.Equal(t, 3, prov.runCalls)
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
	assert.Equal(t, 2, prov.runCalls)
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
	assert.Equal(t, 1, prov.runCalls)
	assert.Equal(t, 1, len(repo.Trays))
}

func TestCreateTray_ProviderError(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	prov := &mockProvider{name: "docker", runErr: errors.New("docker failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	trayType := &config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	err := tm.CreateTray(context.Background(), trayType)
	assert.Error(t, err)
	assert.Equal(t, 0, len(repo.Trays))
}

func TestCreateTray_SaveError_CleansUp(t *testing.T) {
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
	assert.Equal(t, 1, len(prov.cleaned))
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
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	prov := &mockProvider{name: "docker", cleanErr: errors.New("clean failed")}
	tm := newTestManager(repo, &mockProviderFactory{provider: prov})

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
	assert.Equal(t, 1, len(repo.Trays))
}

func TestDeleteTray_FactoryError(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", TrayTypeName: "test-type"}
	factory := &mockProviderFactory{forTrayErr: errors.New("no provider")}
	tm := newTestManager(repo, factory)

	tray, err := tm.DeleteTray(context.Background(), "tray-1")
	assert.Error(t, err)
	assert.Nil(t, tray)
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

	tm.HandleStale(ctx)

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
	tm.HandleStale(ctx)

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
	tm.HandleStale(ctx)
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
