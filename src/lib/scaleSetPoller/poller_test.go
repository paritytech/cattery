package scaleSetPoller

import (
	"cattery/lib/config"
	"cattery/lib/testutil"
	"cattery/lib/trayManager"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
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

func TestNewPollerInitializesHistory(t *testing.T) {
	poller := NewPoller(nil, &config.TrayType{Name: "test-type"}, nil)
	require.NotNil(t, poller.History())
}
