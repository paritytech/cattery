package jobQueue

import (
	"cattery/lib/config"
	"cattery/lib/jobs"
	"cattery/lib/jobs/reposiroties"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	log "github.com/sirupsen/logrus"
)

type QueueManager struct {
	traysStore repositories.ITrayRepository
	jobsStore  reposiroties.IJobRepository
}

func NewQueueManager() *QueueManager {
	return &QueueManager{
		traysStore: repositories.NewMongodbTrayRepository("mongodb://localhost:27017"),
		jobsStore:  reposiroties.NewMongodbJobRepository("mongodb://localhost:27017"),
	}
}

func (qm *QueueManager) Enqueue(job *jobs.Job) error {
	err := qm.jobsStore.Save(job)
	if err != nil {
		return err
	}

	err = qm.Reconcile()
	if err != nil {
		log.Errorf("Error reconciling jobs: %v", err)
	}

	return nil
}

func (qm *QueueManager) CancelJob(jobId int64) error {
	err := qm.jobsStore.Delete(jobId)
	if err != nil {
		return err
	}

	err = qm.Reconcile()
	if err != nil {
		log.Errorf("Error reconciling jobs: %v", err)
	}

	return nil
}

func (qm *QueueManager) Reconcile() error {

	var groupedJobs = qm.jobsStore.GetGroupByLabels()
	var groupedTrays = qm.traysStore.GetGroupByLabels()

	for labels, jobGroup := range groupedJobs {
		var trayType = getTrayType([]string{labels})

		var traysCount = 0

		if trayGroup, ok := groupedTrays[labels]; ok {
			traysCount = len(trayGroup)
		}

		var availableTraysCount = trayType.Limit - traysCount

		if availableTraysCount > len(jobGroup) {
			err := qm.createTrays(trayType, len(jobGroup))
			if err != nil {
				return err
			}
		} else {
			err := qm.createTrays(trayType, availableTraysCount)
			if err != nil {
				return err
			}
		}

	}

	return nil
}

func getTrayType(labels []string) *config.TrayType {
	if len(labels) != 1 {
		// Cattery only support one label for now
		return nil
	}

	// find tray type based on labels (runs_on)
	var label = labels[0]
	var trayType = config.AppConfig.GetTrayType(label)
	if trayType == nil {
		return nil
	}

	//if trayType.GitHubOrg != job.Organization {
	//	return nil
	//}
	return trayType
}

func (qm *QueueManager) createTrays(trayType *config.TrayType, n int) error {
	for i := 0; i < n; i++ {
		err := qm.createTray(trayType)
		if err != nil {
			return err
		}
	}
	return nil
}

func (qm *QueueManager) createTray(trayType *config.TrayType) error {

	provider, err := providers.GetProvider(trayType.Provider)
	if err != nil {
		return err
	}

	//var organizationName = webhookData.GetOrg().GetLogin()
	tray := trays.NewTray(
		[]string{trayType.Name},
		*trayType)

	_ = qm.traysStore.Save(tray)

	err = provider.RunTray(tray)
	if err != nil {
		log.Errorf("Error creating tray for provider: %s, tray: %s: %v", tray.Provider(), tray.Id(), err)
		return err
	}

	return nil
}
