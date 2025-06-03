package trayManager

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
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

func (tm *TrayManager) CreateTrays(trayType *config.TrayType, n int) error {
	for i := 0; i < n; i++ {

		log.Infof("Creating tray %d for type: %s", i+1, trayType.Name)

		// Check if the maximum number of trays for this type has been reached
		count, err := tm.trayRepository.CountByTrayType(trayType.Name)
		if err != nil {
			log.Errorf("Error counting trays for type %s: %v", trayType.Name, err)
			return err
		}

		if count >= trayType.MaxTrays {
			log.Debugf("Maximum number of trays for type %s reached: %d", trayType.Name, count)
			continue
		}

		err = tm.CreateTray(trayType)
		if err != nil {
			return err
		}
	}
	return nil
}

func (tm *TrayManager) CreateTray(trayType *config.TrayType) error {

	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		return err
	}

	tray := trays.NewTray(*trayType)

	_ = tm.trayRepository.Save(tray)

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
		log.Errorf("Failed to set tray %s as 'registered', %s", trayId, err)
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
