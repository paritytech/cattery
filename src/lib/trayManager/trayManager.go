package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/metrics"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// RunnerStatusSource answers whether GitHub considers a runner busy. It is the
// authority on "is this tray doing work?", independent of cattery's own tray
// status.
type RunnerStatusSource interface {
	// IsRunnerBusy reports whether the runner is executing a job. It must
	// return githubClient.ErrRunnerNotFound when GitHub has no such runner.
	IsRunnerBusy(orgName string, runnerId int64) (bool, error)
}

// PollerHealth reports when a tray type's scale set listener last heard from
// GitHub. The second return is false when this replica has no answer, which
// is not the same as the listener being unhealthy.
type PollerHealth interface {
	LastSuccessfulPoll(trayTypeName string) (time.Time, bool)
}

type TrayManager struct {
	trayRepository  repositories.TrayRepository
	providerFactory providers.TrayProviderFactory

	// Both are optional. Unset, the tray lifecycle behaves as it did before
	// the guards existed: a tray past its threshold is deleted on local status
	// alone.
	runnerStatus RunnerStatusSource
	pollerHealth PollerHealth
}

// Option configures optional TrayManager dependencies.
type Option func(*TrayManager)

// WithRunnerStatus supplies the GitHub busy check used before destroying a
// tray that looks stale.
func WithRunnerStatus(rs RunnerStatusSource) Option {
	return func(tm *TrayManager) { tm.runnerStatus = rs }
}

// WithPollerHealth supplies the listener liveness check used before destroying
// a tray that looks stale.
func WithPollerHealth(ph PollerHealth) Option {
	return func(tm *TrayManager) { tm.pollerHealth = ph }
}

func NewTrayManager(trayRepository repositories.TrayRepository, providerFactory providers.TrayProviderFactory, opts ...Option) *TrayManager {
	tm := &TrayManager{
		trayRepository:  trayRepository,
		providerFactory: providerFactory,
	}
	for _, opt := range opts {
		opt(tm)
	}
	return tm
}

func (tm *TrayManager) createTrays(ctx context.Context, trayType *config.TrayType, count int) error {
	maxParallel := trayType.MaxParallelCreation
	if maxParallel <= 0 {
		maxParallel = config.DefaultMaxParallelCreation
	}

	results := tm.createTraysParallel(ctx, trayType, count, maxParallel)
	return tm.logCreationResults(trayType.Name, results)
}

// createTraysParallel creates trays concurrently, limited to maxParallel at a time.
// Returns a slice of errors, one per tray (nil means success).
func (tm *TrayManager) createTraysParallel(ctx context.Context, trayType *config.TrayType, count int, maxParallel int) []error {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxParallel)
	errors := make([]error, count)

	for i := 0; i < count; i++ {
		semaphore <- struct{}{} // block if maxParallel goroutines are already running
		wg.Add(1)

		go func(index int) {
			defer wg.Done()
			defer func() { <-semaphore }()

			log.Infof("Creating tray %d/%d for type: %s", index+1, count, trayType.Name)
			errors[index] = tm.CreateTray(ctx, trayType)
		}(i)
	}

	wg.Wait()
	return errors
}

func (tm *TrayManager) logCreationResults(trayTypeName string, results []error) error {
	total := len(results)
	failed := 0

	for _, err := range results {
		if err != nil {
			log.Errorf("Failed to create tray for type %s: %v", trayTypeName, err)
			failed++
		}
	}

	if failed == total {
		return fmt.Errorf("all %d tray creations failed for type %s", total, trayTypeName)
	}
	if failed > 0 {
		log.Warnf("%d out of %d tray creations failed for type %s", failed, total, trayTypeName)
	}

	return nil
}

// CreateTray reserves a tray row before any provider call so that an agent
// booting on the new VM can register against an existing record. The deploy
// runs in two phases:
//
//  1. StartDeploy submits the create request and populates tray.ProviderData
//     with cleanup-relevant fields. We persist that data immediately so a
//     crash during WaitDeploy still leaves enough info for cleanup.
//
//  2. WaitDeploy blocks until the resource is ready. Concurrent unregisters
//     during either phase flip the row to TrayStatusDeleting; we observe
//     that on the post-WaitDeploy persist and trigger cleanup. We don't
//     check status mid-deploy — at worst we waste the wait phase.
func (tm *TrayManager) CreateTray(ctx context.Context, trayType *config.TrayType) error {
	provider, err := tm.providerFactory.GetProvider(trayType.Provider)
	if err != nil {
		return fmt.Errorf("failed to get provider for type %s: %w", trayType.Name, err)
	}

	tray, err := trays.NewTray(*trayType)
	if err != nil {
		return err
	}

	if err := tm.trayRepository.Save(ctx, tray); err != nil {
		return fmt.Errorf("failed to save tray %s: %w", tray.Id, err)
	}

	if err := provider.StartDeploy(ctx, tray); err != nil {
		log.Errorf("Failed start deploy for tray %s: %v", tray.Id, err)
		metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "create")
		// Persist any provider data the failed StartDeploy populated (e.g.,
		// nomad's parentJobId for leaked-child recovery) before DeleteTray
		// reloads the row and dispatches CleanTray on it.
		if _, pErr := tm.trayRepository.SetProviderData(ctx, tray.Id, tray.ProviderData); pErr != nil {
			log.Errorf("Failed to persist provider data after start deploy error for tray %s: %v", tray.Id, pErr)
		}
		if _, dErr := tm.DeleteTray(ctx, tray.Id); dErr != nil {
			log.Errorf("Failed to delete tray %s after start deploy error: %v", tray.Id, dErr)
		}
		return err
	}

	if _, err := tm.trayRepository.SetProviderData(ctx, tray.Id, tray.ProviderData); err != nil {
		log.Errorf("Failed to persist provider data for tray %s: %v", tray.Id, err)
	}

	waitErr := provider.WaitDeploy(ctx, tray)
	merged, _ := tm.trayRepository.SetProviderData(ctx, tray.Id, tray.ProviderData)

	if waitErr != nil {
		log.Errorf("Failed wait deploy for tray %s: %v", tray.Id, waitErr)
		metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "create")
		if _, dErr := tm.DeleteTray(ctx, tray.Id); dErr != nil {
			log.Errorf("Failed to delete tray %s after wait deploy error: %v", tray.Id, dErr)
		}
		return waitErr
	}

	if merged != nil && merged.Status == trays.TrayStatusDeleting {
		log.Infof("Tray %s marked for deletion during deploy; cleaning up", tray.Id)
		if _, dErr := tm.DeleteTray(ctx, tray.Id); dErr != nil {
			log.Errorf("Failed to delete tray %s after concurrent unregister: %v", tray.Id, dErr)
		}
	}

	return nil
}

func (tm *TrayManager) GetTrayById(ctx context.Context, trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.GetById(ctx, trayId)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Debugf("Tray '%s' not found", trayId)
		return nil, nil
	}
	return tray, nil
}

func (tm *TrayManager) Registering(ctx context.Context, trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRegistering, 0, 0, 0, "", "", "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, fmt.Errorf("failed to update tray status for tray '%s'", trayId)
	}
	return tray, nil
}

func (tm *TrayManager) Registered(ctx context.Context, trayId string, ghRunnerId int64) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRegistered, 0, 0, ghRunnerId, "", "", "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, fmt.Errorf("failed to update tray status for tray '%s'", trayId)
	}
	return tray, nil
}

func (tm *TrayManager) SetJob(ctx context.Context, trayId string, jobRunId int64, workflowRunId int64, repository string, jobName string, workflowName string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRunning, jobRunId, workflowRunId, 0, repository, jobName, workflowName)
	if err != nil {
		return nil, err
	}
	return tray, nil
}

// DeleteTray marks the tray as deleting and attempts to clean up the
// upstream resource. On cleanup failure the row is left in deleting state for
// the stale handler to retry; only repository-level failures propagate to the
// caller. Callers (unregister handler, stale loop) should treat a non-error
// return as "deletion was requested," not "the upstream resource is gone."
func (tm *TrayManager) DeleteTray(ctx context.Context, trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusDeleting, 0, 0, 0, "", "", "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, nil
	}

	provider, err := tm.providerFactory.GetProviderForTray(tray)
	if err != nil {
		log.Errorf("Failed to get provider for tray %s: %v; leaving for stale handler", tray.Id, err)
		return tray, nil
	}

	if err := provider.CleanTray(ctx, tray); err != nil {
		log.Errorf("Failed to clean tray %s; leaving for stale handler: %v", tray.Id, err)
		metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "delete")
		return tray, nil
	}

	if err := tm.trayRepository.Delete(ctx, trayId); err != nil {
		return tray, err
	}

	return tray, nil
}

// HandleStale periodically deletes trays stuck in a non-running status for
// longer than the configured threshold. It blocks until ctx is done.
//
// It must run on a single replica at a time (the caller gates it behind
// leader election): two replicas would each pick up the same stale trays and
// call the provider's cleanup twice.
func (tm *TrayManager) HandleStale(ctx context.Context) {
	cfg := config.Get().Stale.WithDefaults()
	thresholds := resolveStaleThresholds(cfg.Thresholds)

	log.Infof("Stale handler starting: pollInterval=%s thresholds=%v pollerGrace=%s hardThreshold=%s",
		cfg.PollInterval, formatThresholds(thresholds), cfg.PollerGrace, cfg.HardThreshold)
	if tm.runnerStatus == nil {
		log.Warn("Stale handler has no GitHub runner status source: trays will be " +
			"deleted on local status alone, which can destroy a VM running a job")
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Stale handler shutting down")
			return
		case <-ticker.C:
			stale, err := tm.trayRepository.GetStale(ctx, thresholds)
			if err != nil {
				log.Errorf("Failed to get stale trays: %v", err)
				continue
			}

			if len(stale) > 0 {
				log.Infof("Found %d stale trays: %v", len(stale), stale)
			}

			for _, tray := range stale {
				if keep, reason := tm.KeepStale(tray, cfg); keep {
					log.Infof("Keeping stale tray %s (status=%s): %s", tray.Id, tray.Status, reason)
					metrics.StaleTraysKeptInc(tray.GitHubOrgName, tray.TrayTypeName, string(reason))
					continue
				}
				log.Debugf("Deleting stale tray: %s (status=%s)", tray.Id, tray.Status)
				if _, err := tm.DeleteTray(ctx, tray.Id); err != nil {
					log.Errorf("Failed to delete tray %s: %v", tray.Id, err)
				}
				metrics.StaleTraysInc(tray.GitHubOrgName, tray.TrayTypeName)
			}
		}
	}
}

// KeepReason explains why a tray that looks stale was kept alive.
// The empty value means it was not kept.
type KeepReason string

const (
	// KeepReasonNone means no guard objected to deleting the tray.
	KeepReasonNone KeepReason = ""
	// KeepReasonBusy means GitHub reports the runner as executing a job.
	KeepReasonBusy KeepReason = "runner busy on github"
	// KeepReasonBlindListener means the tray type's listener has not polled
	// recently, so the tray status it feeds cannot be trusted.
	KeepReasonBlindListener KeepReason = "scale set listener not polling"
	// KeepReasonUnknown means the busy check could not reach a verdict.
	KeepReasonUnknown KeepReason = "runner status unknown"
)

// KeepStale reports whether a tray that has passed its staleness threshold
// must be kept anyway.
//
// It exists because tray status is a lagging indicator. A tray moves to
// "running" only when the scale set listener delivers a JobStarted message,
// so a listener stuck mid-poll leaves a tray that is building code looking
// like a tray that is doing nothing. Deleting on status alone destroys the
// VM under a live job, and the job fails ~10 minutes later with "the
// self-hosted runner lost communication with the server".
//
// Guards, in order:
//
//   - A tray with no GitHub runner ID never reached GitHub (status "creating"
//     or "registering"), so it cannot hold a job and is deleted.
//   - A blind listener keeps the tray: its status may simply be out of date.
//   - GitHub reporting the runner busy keeps the tray, with no time limit —
//     the job is real and may legitimately run for hours.
//   - A busy check that errors keeps the tray, because "I could not tell" is
//     not "idle".
//
// The last two guards are bounded by Stale.HardThreshold so that a broken
// guard degrades into the old behaviour rather than leaking VMs forever. A
// positively-busy runner is exempt from that bound.
func (tm *TrayManager) KeepStale(tray *trays.Tray, cfg config.StaleConfig) (bool, KeepReason) {
	// No runner on GitHub means nothing could have been scheduled onto it.
	if tray.GitHubRunnerId == 0 {
		return false, KeepReasonNone
	}

	age := time.Since(tray.StatusChanged)
	pastHardLimit := cfg.HardThreshold > 0 && age > cfg.HardThreshold

	if tm.listenerBlind(tray, cfg.PollerGrace) {
		if pastHardLimit {
			log.Warnf("Tray %s: listener for '%s' still not polling after %s, deleting anyway",
				tray.Id, tray.TrayTypeName, age.Truncate(time.Second))
			return false, KeepReasonNone
		}
		return true, KeepReasonBlindListener
	}

	if tm.runnerStatus == nil {
		return false, KeepReasonNone
	}

	busy, err := tm.runnerStatus.IsRunnerBusy(tray.GitHubOrgName, tray.GitHubRunnerId)
	switch {
	case err == nil && busy:
		// No hard limit here on purpose: GitHub says a job is running.
		return true, KeepReasonBusy
	case err == nil:
		return false, KeepReasonNone
	case errors.Is(err, githubClient.ErrRunnerNotFound):
		// GitHub has forgotten the runner, so it is certainly not working.
		return false, KeepReasonNone
	case pastHardLimit:
		log.Warnf("Tray %s: runner status still unavailable after %s, deleting anyway: %v",
			tray.Id, age.Truncate(time.Second), err)
		return false, KeepReasonNone
	default:
		if githubClient.IsForbidden(err) {
			log.Warnf("Tray %s: runner busy check forbidden — the GitHub App needs "+
				"organization 'Self-hosted runners: read': %v", tray.Id, err)
		} else {
			log.Warnf("Tray %s: runner busy check failed, keeping tray: %v", tray.Id, err)
		}
		return true, KeepReasonUnknown
	}
}

// listenerBlind reports whether the tray type's listener has gone quiet for
// longer than grace. A replica with no local poller for the tray type has no
// opinion and reports false, leaving the decision to the GitHub busy check.
func (tm *TrayManager) listenerBlind(tray *trays.Tray, grace time.Duration) bool {
	if tm.pollerHealth == nil || grace <= 0 {
		return false
	}
	last, ok := tm.pollerHealth.LastSuccessfulPoll(tray.TrayTypeName)
	if !ok {
		return false
	}
	return time.Since(last) > grace
}

// resolveStaleThresholds converts the string-keyed config map into a
// TrayStatus-keyed map. Unknown or invalid status names are logged and
// skipped; "running" is rejected since running trays are never stale.
func resolveStaleThresholds(in map[string]time.Duration) map[trays.TrayStatus]time.Duration {
	out := make(map[trays.TrayStatus]time.Duration, len(in))
	for name, d := range in {
		status, err := trays.TrayStatusFromString(name)
		if err != nil {
			log.Warnf("Ignoring stale threshold for unknown status %q", name)
			continue
		}
		if status == trays.TrayStatusRunning {
			log.Warnf("Ignoring stale threshold for status %q: running trays are never stale", name)
			continue
		}
		if d <= 0 {
			log.Warnf("Ignoring non-positive stale threshold for status %q: %s", name, d)
			continue
		}
		out[status] = d
	}
	return out
}

func formatThresholds(m map[trays.TrayStatus]time.Duration) map[string]time.Duration {
	out := make(map[string]time.Duration, len(m))
	for s, d := range m {
		out[s.String()] = d
	}
	return out
}

// ScaleForDemand scales trays for a given tray type based on the desired runner count.
// Follows ARC's pattern: scale up when needed, let HandleJobCompleted and the stale
// handler take care of scale-down. No ghost detection — trust local tray state.
func (tm *TrayManager) ScaleForDemand(ctx context.Context, trayType *config.TrayType, desiredCount int) error {
	activeCount, err := tm.CountTrays(ctx, trayType.Name)
	if err != nil {
		return err
	}

	if desiredCount <= activeCount {
		return nil
	}

	traysToCreate := min(desiredCount-activeCount, trayType.MaxTrays-activeCount)
	if traysToCreate > 0 {
		return tm.createTrays(ctx, trayType, traysToCreate)
	}
	return nil
}

// CountTrays returns the number of active (non-deleting) trays for a given tray type.
func (tm *TrayManager) CountTrays(ctx context.Context, trayTypeName string) (int, error) {
	return tm.trayRepository.CountActive(ctx, trayTypeName)
}

// ListTrays returns all trays sorted by most recently changed.
func (tm *TrayManager) ListTrays(ctx context.Context) ([]*trays.Tray, error) {
	return tm.trayRepository.List(ctx)
}
