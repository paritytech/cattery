package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/jobQueue"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"errors"
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

func (tm *TrayManager) createTrays(trayType *config.TrayType, n int) error {
	for i := 0; i < n; i++ {
		log.Infof("Creating tray %d for type: %s", i+1, trayType.Name)
		err := tm.CreateTray(trayType)
		if err != nil {
			return err
		}
	}
	return nil
}

func (tm *TrayManager) CreateTray(trayType *config.TrayType) error {

	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get provider for type %s: %v", trayType.Name, err)
		log.Error(errMsg)
		return errors.New(errMsg)
	}

	tray := trays.NewTray(*trayType)

	err = provider.RunTray(tray)
	if err != nil {
		log.Errorf("Failed to run tray for provider '%s', tray '%s': %v", trayType.Provider, tray.GetId(), err)
		return err
	}

	err = tm.trayRepository.Save(tray)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to save tray %s: %v", trayType.Name, err)
		log.Error(errMsg)
		return errors.New(errMsg)
	}

	return nil
}

func (tm *TrayManager) GetTrayById(trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.GetById(trayId)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Debugf("Tray '%s' not found", trayId)
		return nil, nil
	}
	return tray, nil
}

func (tm *TrayManager) Registering(trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRegistering, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		var errorMsg = fmt.Sprintf("Failed to update tray status for tray '%s'", trayId)
		return nil, errors.New(errorMsg)
	}

	return tray, nil
}

func (tm *TrayManager) Registered(trayId string, ghRunnerId int64) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRegistered, 0, 0, ghRunnerId)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		var errorMsg = fmt.Sprintf("Failed to update tray status for tray '%s'", trayId)
		return nil, errors.New(errorMsg)
	}

	return tray, nil
}

func (tm *TrayManager) SetJob(trayId string, jobRunId int64, workflowRunId int64) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRunning, jobRunId, workflowRunId, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		var errorMsg = fmt.Sprintf("Failed to update tray status for tray '%s'", trayId)
		return nil, errors.New(errorMsg)
	}

	return tray, nil
}

func (tm *TrayManager) DeleteTray(trayId string) (*trays.Tray, error) {

	var tray, err = tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusDeleting, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		return nil, nil // Tray not found, nothing to delete
	}

	ghClient, err := githubClient.NewGithubClientWithOrgName(tray.GetGitHubOrgName())
	if err != nil {
		return nil, err
	}

	err = ghClient.RemoveRunner(tray.GitHubRunnerId)
	if err != nil {
		return nil, err
	}

	provider, err := providers.GetProviderForTray(tray)
	if err != nil {
		return nil, err
	}

	err = provider.CleanTray(tray)
	if err != nil {
		log.Errorf("Failed to delete tray for provider %s, tray %s: %v", provider.GetProviderName(), tray.GetId(), err)
		return nil, err
	}

	err = tm.trayRepository.Delete(trayId)
	if err != nil {
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) HandleStale(ctx context.Context) {

	var interval = time.Minute * 5

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:

				time.Sleep(interval / 2)

				stale, err := tm.trayRepository.GetStale(interval, interval*2)
				if err != nil {
					log.Errorf("Failed to get stale trays: %v", err)
					continue
				}

				log.Infof("Found %d stale trays: %v", len(stale), stale)

				for _, tray := range stale {
					log.Debugf("Deleting stale tray: %s", tray.GetId())

					_, err := tm.DeleteTray(tray.GetId())
					if err != nil {
						log.Errorf("Failed to delete tray %s: %v", tray.GetId(), err)
					}
				}
			}
		}
	}()
}

func (tm *TrayManager) HandleJobsQueue(ctx context.Context, manager *jobQueue.QueueManager) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var groups = manager.GetJobsCount()
				for typeName, jobsCount := range groups {
					err := tm.handleType(typeName, jobsCount)
					if err != nil {
						log.Error(err)
					}
				}

				time.Sleep(10 * time.Second)
			}
		}
	}()
}

func (tm *TrayManager) handleType(trayTypeName string, jobsInQueue int) error {
	// log.Debugf("Handling tray type %s with %d jobs in queue", trayTypeName, jobsInQueue)
	countByStatus, total, err := tm.trayRepository.CountByTrayType(trayTypeName)
	if err != nil {
		log.Errorf("Failed to count trays for type %s: %v", trayTypeName, err)
		return err
	}

	var traysWithNoJob = countByStatus[trays.TrayStatusCreating] + countByStatus[trays.TrayStatusRegistering] + countByStatus[trays.TrayStatusRegistered]
	// log.Debugf("Tray type %s has %d trays, %d with no job", trayTypeName, total, traysWithNoJob)
	if jobsInQueue > traysWithNoJob {
		var trayType = getTrayType(trayTypeName)
		if trayType == nil {
			log.Warnf("Tray type '%s' not found in config; skipping creation", trayTypeName)
			return nil
		}

		var remainingTrays = trayType.MaxTrays - total
		var traysToCreate = jobsInQueue - traysWithNoJob
		if traysToCreate > remainingTrays {
			traysToCreate = remainingTrays
		}

		err := tm.createTrays(trayType, traysToCreate)
		if err != nil {
			return err
		}
	}

	if jobsInQueue < traysWithNoJob {
		var traysToDelete = traysWithNoJob - jobsInQueue
		redundant, err := tm.trayRepository.MarkRedundant(trayTypeName, traysToDelete)
		if err != nil {
			return err
		}

		for _, tray := range redundant {
			if _, delErr := tm.DeleteTray(tray.Id); delErr != nil {
				log.Errorf("Failed to delete redundant tray %s: %v", tray.Id, delErr)
			}
		}

	}

	return nil
}

func getTrayType(trayTypeName string) *config.TrayType {
	var trayType = config.AppConfig.GetTrayType(trayTypeName)
	return trayType
}
