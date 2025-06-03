package jobQueue

import (
	"cattery/lib/config"
	"cattery/lib/jobs"
	"cattery/lib/trayManager"
	"context"
	"errors"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sync"
)

type QueueManager struct {
	trayManager *trayManager.TrayManager
	jobQueue    *JobQueue
	waitGroup   sync.WaitGroup
	listen      bool

	collection   *mongo.Collection
	changeStream *mongo.ChangeStream
}

func NewQueueManager(trayManager *trayManager.TrayManager, listen bool) *QueueManager {
	return &QueueManager{
		trayManager: trayManager,
		jobQueue:    NewJobQueue(),
		waitGroup:   sync.WaitGroup{},
		listen:      listen,
	}
}

func (qm *QueueManager) Connect(collection *mongo.Collection) {
	qm.collection = collection
}

func (qm *QueueManager) Load() error {
	qm.waitGroup.Add(1)
	defer qm.waitGroup.Done()

	collection := qm.collection

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
	_, err := qm.collection.InsertOne(context.Background(), job)
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

	err := qm.deleteJob(jobId)
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
	_, err := qm.collection.DeleteOne(context.Background(), bson.M{"id": jobId})
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) Reconcile(trayTypeName string) error {

	var trayType = getTrayType(trayTypeName)

	var jobsInQueue = len(qm.jobQueue.GetGroup(trayTypeName))

	err := qm.trayManager.CreateTrays(trayType, jobsInQueue)
	if err != nil {
		return err
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
