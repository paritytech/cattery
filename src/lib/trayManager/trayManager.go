package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/metrics"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type TrayManager struct {
	trayRepository  repositories.TrayRepository
	providerFactory providers.TrayProviderFactory
}

func NewTrayManager(trayRepository repositories.TrayRepository, providerFactory providers.TrayProviderFactory) *TrayManager {
	return &TrayManager{
		trayRepository:  trayRepository,
		providerFactory: providerFactory,
	}
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

func (tm *TrayManager) HandleStale(ctx context.Context) {
	cfg := config.Get().Stale.WithDefaults()
	thresholds := resolveStaleThresholds(cfg.Thresholds)

	log.Infof("Stale handler starting: pollInterval=%s thresholds=%v", cfg.PollInterval, formatThresholds(thresholds))

	go func() {
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
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
					log.Debugf("Deleting stale tray: %s (status=%s)", tray.Id, tray.Status)
					if _, err := tm.DeleteTray(ctx, tray.Id); err != nil {
						log.Errorf("Failed to delete tray %s: %v", tray.Id, err)
					}
					metrics.StaleTraysInc(tray.GitHubOrgName, tray.TrayTypeName)
				}
			}
		}
	}()
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
