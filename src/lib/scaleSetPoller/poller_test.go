package scaleSetPoller

import (
	"cattery/lib/config"
	"cattery/lib/testutil"
	"cattery/lib/trayManager"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingProvider struct {
	waitStarted chan struct{}
	release     chan struct{}
	waitOnce    sync.Once
}

func (p *blockingProvider) GetProviderName() string { return "blocking" }

func (p *blockingProvider) StartDeploy(_ context.Context, _ *trays.Tray) error {
	return nil
}

func (p *blockingProvider) WaitDeploy(ctx context.Context, _ *trays.Tray) error {
	p.waitOnce.Do(func() { close(p.waitStarted) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *blockingProvider) CleanTray(_ context.Context, _ *trays.Tray) error {
	return nil
}

type pollerTestProviderFactory struct {
	provider providers.TrayProvider
}

func (f *pollerTestProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	return f.provider, nil
}

func (f *pollerTestProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	return f.provider, nil
}

func TestHandleDesiredRunnerCount_RecordsScaleBeforeProviderWaitCompletes(t *testing.T) {
	provider := &blockingProvider{
		waitStarted: make(chan struct{}),
		release:     make(chan struct{}),
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })

	trayType := &config.TrayType{
		Name:     "test-type",
		Provider: "blocking",
		MaxTrays: 1,
	}
	tm := trayManager.NewTrayManager(
		testutil.NewMockTrayRepository(),
		&pollerTestProviderFactory{provider: provider},
	)
	poller := &Poller{
		trayType:    trayType,
		trayManager: tm,
		history:     &History{},
	}
	scaler := &catteryScaler{poller: poller}
	scaler.latestStat.Store(&scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs:     1,
		TotalAssignedJobs:      1,
		TotalRunningJobs:       0,
		TotalBusyRunners:       0,
		TotalIdleRunners:       0,
		TotalRegisteredRunners: 0,
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := scaler.HandleDesiredRunnerCount(context.Background(), 1)
		errCh <- err
	}()

	select {
	case <-provider.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("provider wait did not start")
	}

	got := poller.History().Recent()
	require.Len(t, got, 1)
	assert.Equal(t, MessageKindScale, got[0].Kind)
	assert.Equal(t, "test-type", got[0].TrayType)
	assert.Equal(t, 1, got[0].DesiredCount)
	require.NotNil(t, got[0].Stats)
	assert.Equal(t, 1, got[0].Stats.Available)
	assert.Equal(t, 1, got[0].Stats.Assigned)

	releaseOnce.Do(func() { close(provider.release) })
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("HandleDesiredRunnerCount did not finish")
	}
}

// The scaleset listener acks a message before invoking the scaler callbacks
// and aborts the whole session on any callback error. Per-tray failures (DB or
// provider) must therefore be swallowed: returning them would drop every job
// event until the session is recreated.
func TestScalerCallbacks_PerTrayErrorsDoNotFailSession(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.UpdateErr = errors.New("mongo unavailable")
	repo.CountErr = errors.New("mongo unavailable")

	trayType := &config.TrayType{Name: "test-type", Provider: "blocking", MaxTrays: 1}
	tm := trayManager.NewTrayManager(repo, &pollerTestProviderFactory{provider: &blockingProvider{}})
	poller := &Poller{
		trayType:    trayType,
		trayManager: tm,
		history:     &History{},
		logger:      log.WithField("test", t.Name()),
	}
	scaler := &catteryScaler{poller: poller}
	ctx := context.Background()

	err := scaler.HandleJobStarted(ctx, &scaleset.JobStarted{
		RunnerName:     "test-type-abc",
		JobMessageBase: scaleset.JobMessageBase{WorkflowRunID: 42, JobID: "7"},
	})
	assert.NoError(t, err, "SetJob failure must not propagate to the listener")

	err = scaler.HandleJobCompleted(ctx, &scaleset.JobCompleted{
		RunnerName:     "test-type-abc",
		JobMessageBase: scaleset.JobMessageBase{WorkflowRunID: 42, JobID: "7"},
	})
	assert.NoError(t, err, "DeleteTray failure must not propagate to the listener")

	count, err := scaler.HandleDesiredRunnerCount(ctx, 1)
	assert.NoError(t, err, "scaling failure must not propagate to the listener")
	assert.Equal(t, 0, count)

	// The events are still recorded so the status page reflects what GitHub sent.
	kinds := []MessageKind{}
	for _, m := range poller.History().Recent() {
		kinds = append(kinds, m.Kind)
	}
	assert.ElementsMatch(t, []MessageKind{MessageKindJobStarted, MessageKindJobCompleted, MessageKindScale}, kinds)
}

func TestNewPollerInitializesHistory(t *testing.T) {
	poller := NewPoller(nil, &config.TrayType{Name: "test-type"}, nil)
	require.NotNil(t, poller.History())
}
