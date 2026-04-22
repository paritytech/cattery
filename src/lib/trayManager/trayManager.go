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

func (tm *TrayManager) CreateTray(ctx context.Context, trayType *config.TrayType) error {
	provider, err := tm.providerFactory.GetProvider(trayType.Provider)
	if err != nil {
		return fmt.Errorf("failed to get provider for type %s: %w", trayType.Name, err)
	}

	tray, err := trays.NewTray(*trayType)
	if err != nil {
		return err
	}

	err = provider.RunTray(tray)
	if err != nil {
		log.Errorf("Failed to run tray for provider '%s', tray '%s': %v", trayType.Provider, tray.Id, err)
		metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "create")
		return err
	}

	err = tm.trayRepository.Save(ctx, tray)
	if err != nil {
		log.Errorf("Failed to save tray %s: %v — cleaning up provider resource", trayType.Name, err)
		if cleanErr := provider.CleanTray(tray); cleanErr != nil {
			log.Errorf("Failed to clean up tray %s after save failure: %v", tray.Id, cleanErr)
			metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "delete")
		}
		return fmt.Errorf("failed to save tray %s: %w", trayType.Name, err)
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
		return nil, err
	}

	err = provider.CleanTray(tray)
	if err != nil {
		log.Errorf("Failed to delete tray for provider %s, tray %s: %v", provider.GetProviderName(), tray.Id, err)
		metrics.TrayProviderErrors(tray.GitHubOrgName, tray.ProviderName, tray.TrayTypeName, "delete")
		return nil, err
	}

	err = tm.trayRepository.Delete(ctx, trayId)
	if err != nil {
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) HandleStale(ctx context.Context) {
	interval := time.Minute * 15

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(interval / 2)

				stale, err := tm.trayRepository.GetStale(ctx, interval)
				if err != nil {
					log.Errorf("Failed to get stale trays: %v", err)
					continue
				}

				if len(stale) > 0 {
					log.Infof("Found %d stale trays: %v", len(stale), stale)
				}

				for _, tray := range stale {
					log.Debugf("Deleting stale tray: %s", tray.Id)
					if _, err := tm.DeleteTray(ctx, tray.Id); err != nil {
						log.Errorf("Failed to delete tray %s: %v", tray.Id, err)
					}
					metrics.StaleTraysInc(tray.GitHubOrgName, tray.TrayTypeName)
				}
			}
		}
	}()
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
