package jobQueue

import (
	"cattery/lib/config"
	"cattery/lib/jobs"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"context"
	"errors"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sync"
)

type QueueManager struct {
	TraysStore *repositories.MongodbTrayRepository
	jobQueue   *JobQueue
	waitGroup  sync.WaitGroup
	listen     bool

	client       *mongo.Client
	changeStream *mongo.ChangeStream
}

func NewQueueManager(listen bool) *QueueManager {
	return &QueueManager{
		TraysStore: repositories.NewMongodbTrayRepository(),
		jobQueue:   NewJobQueue(),
		waitGroup:  sync.WaitGroup{},
		listen:     listen,
	}
}

func (qm *QueueManager) Connect(uri string) error {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(uri).SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		return err
	}

	qm.TraysStore.Connect(client.Database("cattery").Collection("trays"))
	qm.client = client

	return nil
}

func (qm *QueueManager) Load() error {
	qm.waitGroup.Add(1)
	defer qm.waitGroup.Done()

	collection := qm.client.Database("cattery").Collection("jobs")

	if qm.listen {
		changeStream, err := collection.Watch(nil, mongo.Pipeline{}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
		if err != nil {
			return err
		}
		qm.changeStream = changeStream
	}

	allJobs, err := collection.Find(nil, bson.M{})
	if err != nil {
		return err
	}

	for allJobs.Next(nil) {
		var job jobs.Job
		decodeErr := allJobs.Decode(&job)
		if decodeErr != nil {
			return err
		}

		qm.jobQueue.Add(&job)
	}

	if qm.listen {
		go func() {
			for qm.changeStream.Next(nil) {
				var event changeEvent[jobs.Job]
				decodeErr := qm.changeStream.Decode(&event)
				if decodeErr != nil {
					log.Error("Error decoding change stream: ", decodeErr)
					qm.Load()
				}

				var job = event.FullDocument

				switch event.OperationType {
				case "replace":
					fallthrough
				case "update":
					fallthrough
				case "insert":
					qm.jobQueue.Add(&event.FullDocument)
				case "delete":
					qm.jobQueue.Delete(job.Id)
				default:
					log.Warn("Unknown operation type: ", event.OperationType)
				}
			}
		}()
	}

	return nil
}

func (qm *QueueManager) AddJob(job *jobs.Job) error {
	qm.jobQueue.Add(job)
	_, err := qm.client.Database("cattery").Collection("jobs").InsertOne(context.Background(), job)
	if err != nil {
		return err
	}

	err = qm.Reconcile(job.TrayType)
	if err != nil {
		log.Errorf("Error reconciling jobs: %v", err)
	}

	return nil
}

func (qm *QueueManager) JobInProgress(jobId int64, trayId string) error {
	job := qm.jobQueue.Get(jobId)
	if job == nil {
		log.Errorf("No job found with id %v", jobId)
		return errors.New("No job found with id ")
	}

	_, err := qm.TraysStore.UpdateStatus(trayId, trays.TrayStatusRunning, job.Id)
	if err != nil {
		return err
	}

	err = qm.deleteJob(jobId)
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) JobFinished(jobId int64, trayId string) error {
	job := qm.jobQueue.Get(jobId)
	if job == nil {
		log.Errorf("No job found with id %v", jobId)
		return errors.New("No job found with id ")
	}

	err := qm.deleteJob(jobId)
	if err != nil {
		return err
	}

	err = qm.deleteTray(trayId)
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) UpdateJobStatus(jobId int64, status jobs.JobStatus) error {

	job := qm.jobQueue.Get(jobId)
	if job == nil {
		log.Errorf("No job found with id %v", jobId)
		return errors.New("No job found with id ")
	}

	switch status {
	case jobs.JobStatusInProgress:
		err := qm.deleteJob(jobId)
		if err != nil {
			return err
		}
	case jobs.JobStatusFinished:
		err := qm.deleteJob(jobId)
		if err != nil {
			return err
		}
	default:
		return nil
	}

	err := qm.Reconcile(job.TrayType)
	if err != nil {
		log.Errorf("Error reconciling jobs: %v", err)
	}

	return nil
}

func (qm *QueueManager) deleteJob(jobId int64) error {
	qm.jobQueue.Delete(jobId)
	_, err := qm.client.Database("cattery").Collection("jobs").DeleteOne(context.Background(), bson.M{"id": jobId})
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) Reconcile(trayTypeName string) error {

	var trayType = getTrayType(trayTypeName)
	traysCount64, err := qm.TraysStore.CountByTrayType(trayTypeName)
	if err != nil {
		return err
	}

	var jobsInQueue = len(qm.jobQueue.GetGroup(trayTypeName))

	var traysCount = int(traysCount64)
	var availableTraysCount = trayType.Limit - traysCount

	if availableTraysCount > jobsInQueue {
		err := qm.createTrays(trayType, jobsInQueue)
		if err != nil {
			return err
		}
	} else {
		err := qm.createTrays(trayType, availableTraysCount)
		if err != nil {
			return err
		}
	}

	return nil
}

func getTrayType(trayTypeName string) *config.TrayType {

	var trayType = config.AppConfig.GetTrayType(trayTypeName)
	if trayType == nil {
		return nil
	}

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

	tray := trays.NewTray(*trayType)

	_ = qm.TraysStore.Save(tray)

	err = provider.RunTray(tray)
	if err != nil {
		log.Errorf("Error creating tray for provider: %s, tray: %s: %v", tray.Provider(), tray.Id(), err)
		return err
	}

	return nil
}

func (qm *QueueManager) deleteTray(trayId string) error {
	tray, err := qm.TraysStore.Get(trayId)
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
		log.Errorf("Error deleting tray for provider: %s, tray: %s: %v", tray.Provider(), tray.Id(), err)
		return err
	}

	err = qm.TraysStore.Delete(trayId)
	if err != nil {
		return err
	}

	return nil
}
