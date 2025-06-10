package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/jobQueue"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"time"
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
		var errMsg = fmt.Sprintf("Error getting provider for type %s: %v", trayType.Name, err)
		log.Error(errMsg)
		return errors.New(errMsg)
	}

	tray := trays.NewTray(*trayType)

	err = tm.trayRepository.Save(tray)
	if err != nil {
		var errMsg = fmt.Sprintf("Error creating tray %s: %v", trayType.Name, err)
		log.Error(errMsg)
		return errors.New(errMsg)
	}

	err = provider.RunTray(tray)
	if err != nil {
		log.Errorf("Error creating tray for provider: %s, tray: %s: %v", tray.Provider(), tray.GetId(), err)
		return err
	}

	return nil
}

func (tm *TrayManager) SetReady(trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRegistered, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Errorf("Failed to set tray %s as 'registered', tray not found", trayId)
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) Registering(trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRegistered, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Errorf("Failed to set tray %s as 'registering', tray not found", trayId)
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) Registered(trayId string) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRegistered, 0)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Errorf("Failed to set tray %s as 'registered', tray not found", trayId)
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) SetJob(trayId string, jobRunId int64) (*trays.Tray, error) {
	tray, err := tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusRunning, jobRunId)
	if err != nil {
		return nil, err
	}
	if tray == nil {
		log.Errorf("Failed to set jobId %d, tray %s not found", jobRunId, trayId)
		return nil, err
	}

	return tray, nil
}

func (tm *TrayManager) DeleteTray(trayId string) error {

	var tray, err = tm.trayRepository.UpdateStatus(trayId, trays.TrayStatusDeleting, 0)
	if err != nil {
		return err
	}
	if tray == nil {
		return nil // Tray not found, nothing to delete
	}

	provider, err := providers.GetProvider(tray.Provider())
	if err != nil {
		return err
	}

	err = provider.CleanTray(tray)
	if err != nil {
		log.Errorf("Error deleting tray for provider: %s, tray: %s: %v", tray.Provider(), tray.GetId(), err)
		return err
	}

	err = tm.trayRepository.Delete(trayId)
	if err != nil {
		return err
	}

	return nil
}

func (tm *TrayManager) HandleJobsQueue(ctx context.Context, manager *jobQueue.QueueManager) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var groups = manager.GetJobsCount()
				for typeName, jobsCount := range groups {
					err := tm.handleType(typeName, jobsCount)
					if err != nil {
						log.Error(err)
					}
				}
			}
		}
	}()
}

func (tm *TrayManager) handleType(trayTypeName string, jobsInQueue int) error {
	countByStatus, total, err := tm.trayRepository.CountByTrayType(trayTypeName)
	if err != nil {
		log.Errorf("Error counting trays for type %s: %v", trayTypeName, err)
		return err
	}

	if jobsInQueue > countByStatus[trays.TrayStatusCreating] {
		var trayType = getTrayType(trayTypeName)
		//TODO: handle nil

		var remainingTrays = trayType.MaxTrays - total
		var traysToCreate = jobsInQueue - countByStatus[trays.TrayStatusCreating]
		if traysToCreate > remainingTrays {
			traysToCreate = remainingTrays
		}

		err := tm.createTrays(trayType, traysToCreate)
		if err != nil {
			return err
		}
	}

	if jobsInQueue < countByStatus[trays.TrayStatusCreating] {
		var traysToDelete = countByStatus[trays.TrayStatusCreating] - jobsInQueue
		redundant, err := tm.trayRepository.MarkRedundant(trayTypeName, traysToDelete)
		if err != nil {
			return err
		}

		for _, tray := range redundant {
			tm.DeleteTray(tray.Id)
		}

	}

	return nil
}

func getTrayType(trayTypeName string) *config.TrayType {
	var trayType = config.AppConfig.GetTrayType(trayTypeName)
	return trayType
}
