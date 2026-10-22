package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/testutil"
	"cattery/lib/trays"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- Guard mocks ---

type stubRunnerStatus struct {
	busy  bool
	err   error
	calls int
}

func (s *stubRunnerStatus) IsRunnerBusy(_ string, _ int64) (bool, error) {
	s.calls++
	return s.busy, s.err
}

type stubPollerHealth struct {
	last time.Time
	ok   bool
}

func (s *stubPollerHealth) LastSuccessfulPoll(_ string) (time.Time, bool) {
	return s.last, s.ok
}

func staleCfg() config.StaleConfig {
	return config.StaleConfig{}.WithDefaults()
}

// registeredTray is the shape the reaper sees: past its threshold, with a
// runner ID, so every guard applies.
func registeredTray(age time.Duration) *trays.Tray {
	return &trays.Tray{
		Id:             "parity-large-persistent-e32422b3be1bc0b3",
		TrayTypeName:   "parity-large-persistent",
		GitHubOrgName:  "paritytech",
		GitHubRunnerId: 8369063,
		Status:         trays.TrayStatusRegistered,
		StatusChanged:  time.Now().Add(-age),
	}
}

// --- KeepStale ---

func TestKeepStale_NoRunnerIdIsDeleted(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{busy: true}))

	tray := registeredTray(20 * time.Minute)
	tray.GitHubRunnerId = 0

	keep, reason := tm.KeepStale(tray, staleCfg())

	assert.False(t, keep, "a tray that never reached GitHub cannot be running a job")
	assert.Equal(t, KeepReasonNone, reason)
}

func TestKeepStale_BusyRunnerIsKept(t *testing.T) {
	rs := &stubRunnerStatus{busy: true}
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(rs))

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.True(t, keep)
	assert.Equal(t, KeepReasonBusy, reason)
	assert.Equal(t, 1, rs.calls)
}

func TestKeepStale_IdleRunnerIsDeleted(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{busy: false}))

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.False(t, keep)
	assert.Equal(t, KeepReasonNone, reason)
}

// A long job must never be reaped, however long it runs. The hard threshold
// bounds inconclusive guards, not a positive "busy".
func TestKeepStale_BusyRunnerIgnoresHardThreshold(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{busy: true}))

	cfg := staleCfg()
	keep, reason := tm.KeepStale(registeredTray(cfg.HardThreshold+time.Hour), cfg)

	assert.True(t, keep)
	assert.Equal(t, KeepReasonBusy, reason)
}

func TestKeepStale_MissingRunnerIsDeleted(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{err: githubClient.ErrRunnerNotFound}))

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.False(t, keep, "GitHub has forgotten the runner, so it holds no job")
	assert.Equal(t, KeepReasonNone, reason)
}

func TestKeepStale_UnknownStatusIsKept(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{err: errors.New("context deadline exceeded")}))

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.True(t, keep, `"could not tell" must not be read as "idle"`)
	assert.Equal(t, KeepReasonUnknown, reason)
}

// A guard that can never answer must not leak VMs forever.
func TestKeepStale_UnknownStatusGivesUpAtHardThreshold(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(&stubRunnerStatus{err: errors.New("context deadline exceeded")}))

	cfg := staleCfg()
	keep, _ := tm.KeepStale(registeredTray(cfg.HardThreshold+time.Minute), cfg)

	assert.False(t, keep)
}

func TestKeepStale_BlindListenerKeepsTrayWithoutAskingGitHub(t *testing.T) {
	cfg := staleCfg()
	rs := &stubRunnerStatus{busy: false}
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(rs),
		WithPollerHealth(&stubPollerHealth{last: time.Now().Add(-cfg.PollerGrace - time.Minute), ok: true}))

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), cfg)

	assert.True(t, keep)
	assert.Equal(t, KeepReasonBlindListener, reason)
	assert.Zero(t, rs.calls, "a blind listener is decided locally, with no GitHub call")
}

func TestKeepStale_HealthyListenerDefersToGitHub(t *testing.T) {
	rs := &stubRunnerStatus{busy: false}
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(rs),
		WithPollerHealth(&stubPollerHealth{last: time.Now().Add(-10 * time.Second), ok: true}))

	keep, _ := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.False(t, keep)
	assert.Equal(t, 1, rs.calls)
}

// With leader election the poller for a tray type can live on another replica,
// so "no local answer" must not disable the reaper.
func TestKeepStale_UnknownListenerDefersToGitHub(t *testing.T) {
	rs := &stubRunnerStatus{busy: false}
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithRunnerStatus(rs),
		WithPollerHealth(&stubPollerHealth{ok: false}))

	keep, _ := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.False(t, keep)
	assert.Equal(t, 1, rs.calls)
}

func TestKeepStale_BlindListenerGivesUpAtHardThreshold(t *testing.T) {
	cfg := staleCfg()
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{},
		WithPollerHealth(&stubPollerHealth{last: time.Now().Add(-24 * time.Hour), ok: true}))

	keep, _ := tm.KeepStale(registeredTray(cfg.HardThreshold+time.Minute), cfg)

	assert.False(t, keep)
}

// Without the guards wired the behaviour is exactly what it was before.
func TestKeepStale_NoGuardsConfiguredDeletes(t *testing.T) {
	tm := NewTrayManager(testutil.NewMockTrayRepository(), &mockProviderFactory{})

	keep, reason := tm.KeepStale(registeredTray(20*time.Minute), staleCfg())

	assert.False(t, keep)
	assert.Equal(t, KeepReasonNone, reason)
}

// --- Regression: polkadot-sdk run 39572082409 ---

// On 2026-10-21 the listener for parity-large-persistent stalled inside a
// single GetMessage call for 15m44s. The tray running build-rustdoc therefore
// never left "registered", the reaper deleted its VM at the 15-minute mark,
// and the job failed with "the self-hosted runner lost communication with the
// server". Either guard alone is enough to prevent it.
func TestHandleStale_DoesNotDeleteTrayRunningAJob(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server:   config.ServerConfig{ListenAddress: ":8080", AdvertiseUrl: "http://localhost"},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"registered": 15 * time.Minute},
		},
	})

	repo := testutil.NewMockTrayRepository()
	provider := &mockProvider{name: "gce"}
	tray := registeredTray(15*time.Minute + 3*time.Second)
	tray.ProviderName = "gce"
	repo.Trays[tray.Id] = tray
	repo.SetStale([]*trays.Tray{tray}, nil)

	tm := NewTrayManager(repo, &mockProviderFactory{provider: provider},
		// The listener has been stuck mid-poll for the whole window, which is
		// why the tray still reads "registered".
		WithPollerHealth(&stubPollerHealth{last: time.Now().Add(-15 * time.Minute), ok: true}),
		// GitHub knows the runner is building docs.
		WithRunnerStatus(&stubRunnerStatus{busy: true}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.HandleStale(ctx)

	// Several poll cycles must pass without the VM being destroyed.
	assert.Never(t, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.cleaned) > 0
	}, 200*time.Millisecond, 10*time.Millisecond,
		"the VM running build-rustdoc must survive a blind listener")
}

// The counterpart: a genuinely idle tray is still reaped, so the guards did
// not simply disable the reaper.
func TestHandleStale_StillDeletesIdleTray(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server:   config.ServerConfig{ListenAddress: ":8080", AdvertiseUrl: "http://localhost"},
		Database: config.DatabaseConfig{Uri: "x", Database: "y"},
		Stale: config.StaleConfig{
			PollInterval: 10 * time.Millisecond,
			Thresholds:   map[string]time.Duration{"registered": 15 * time.Minute},
		},
	})

	repo := testutil.NewMockTrayRepository()
	provider := &mockProvider{name: "gce"}
	tray := registeredTray(20 * time.Minute)
	tray.ProviderName = "gce"
	repo.Trays[tray.Id] = tray
	repo.SetStale([]*trays.Tray{tray}, nil)

	tm := NewTrayManager(repo, &mockProviderFactory{provider: provider},
		WithPollerHealth(&stubPollerHealth{last: time.Now(), ok: true}),
		WithRunnerStatus(&stubRunnerStatus{busy: false}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.HandleStale(ctx)

	assert.Eventually(t, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.cleaned) == 1
	}, 500*time.Millisecond, 10*time.Millisecond, "an idle tray must still be reaped")
}
