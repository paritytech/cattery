package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/metrics"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

type TrayManager struct {
	trayRepository repositories.ITrayRepository
}

func NewTrayManager(trayRepository repositories.ITrayRepository) *TrayManager {
	return &TrayManager{
		trayRepository: trayRepository,
	}
}

func (tm *TrayManager) createTrays(ctx context.Context, trayType *config.TrayType, n int) error {
	for i := 0; i < n; i++ {
		log.Infof("Creating tray %d for type: %s", i+1, trayType.Name)
		if err := tm.CreateTray(ctx, trayType); err != nil {
			return err
		}
	}
	return nil
}

func (tm *TrayManager) CreateTray(ctx context.Context, trayType *config.TrayType) error {
	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		return fmt.Errorf("failed to get provider for type %s: %w", trayType.Name, err)
	}

	tray := trays.NewTray(*trayType)

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
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRegistering, 0, 0, 0, "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, fmt.Errorf("failed to update tray status for tray '%s'", trayId)
	}
	return tray, nil
}

func (tm *TrayManager) Registered(ctx context.Context, trayId string, ghRunnerId int64) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRegistered, 0, 0, ghRunnerId, "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, fmt.Errorf("failed to update tray status for tray '%s'", trayId)
	}
	return tray, nil
}

func (tm *TrayManager) SetJob(ctx context.Context, trayId string, jobRunId int64, workflowRunId int64, repository string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusRunning, jobRunId, workflowRunId, 0, repository)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, fmt.Errorf("failed to update tray status for tray '%s'", trayId)
	}
	return tray, nil
}

func (tm *TrayManager) DeleteTray(ctx context.Context, trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(ctx, trayId, trays.TrayStatusDeleting, 0, 0, 0, "")
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, nil
	}

	provider, err := providers.GetProviderForTray(tray)
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
	interval := time.Minute * 2

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
// The desiredCount is TotalAssignedJobs from GitHub scale set statistics — the total
// number of runners that should exist (running + idle) to serve all assigned jobs.
func (tm *TrayManager) ScaleForDemand(ctx context.Context, trayType *config.TrayType, desiredCount int) error {
	countByStatus, total, err := tm.trayRepository.CountByTrayType(ctx, trayType.Name)
	if err != nil {
		log.Errorf("Failed to count trays for type %s: %v", trayType.Name, err)
		return err
	}

	idleTrays := countByStatus[trays.TrayStatusCreating] + countByStatus[trays.TrayStatusRegistering] + countByStatus[trays.TrayStatusRegistered]
	activeTotal := total - countByStatus[trays.TrayStatusDeleting]

	if desiredCount > activeTotal {
		remainingCapacity := trayType.MaxTrays - activeTotal
		traysToCreate := desiredCount - activeTotal
		if traysToCreate > remainingCapacity {
			traysToCreate = remainingCapacity
		}
		if traysToCreate > 0 {
			if err := tm.createTrays(ctx, trayType, traysToCreate); err != nil {
				return err
			}
		}
	}

	if desiredCount < activeTotal && idleTrays > 0 {
		excess := activeTotal - desiredCount
		traysToDelete := excess
		if traysToDelete > idleTrays {
			traysToDelete = idleTrays
		}
		redundant, err := tm.trayRepository.MarkRedundant(ctx, trayType.Name, traysToDelete)
		if err != nil {
			return err
		}
		for _, tray := range redundant {
			if _, delErr := tm.DeleteTray(ctx, tray.Id); delErr != nil {
				log.Errorf("Failed to delete redundant tray %s: %v", tray.Id, delErr)
			}
		}
	}

	return nil
}

// CountTrays returns the number of active (non-deleting) trays for a given tray type.
func (tm *TrayManager) CountTrays(ctx context.Context, trayTypeName string) (int, error) {
	countByStatus, total, err := tm.trayRepository.CountByTrayType(ctx, trayTypeName)
	if err != nil {
		return 0, err
	}
	return total - countByStatus[trays.TrayStatusDeleting], nil
}
