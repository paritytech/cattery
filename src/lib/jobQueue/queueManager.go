package jobQueue

import (
	"cattery/lib/jobs"
	"context"
	"errors"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sync"
)

type QueueManager struct {
	jobQueue  *JobQueue
	waitGroup sync.WaitGroup

	collection   *mongo.Collection
	changeStream *mongo.ChangeStream
}

func NewQueueManager() *QueueManager {
	return &QueueManager{
		jobQueue:  NewJobQueue(),
		waitGroup: sync.WaitGroup{},
	}
}

func (qm *QueueManager) Connect(collection *mongo.Collection) {
	qm.collection = collection
}

func (qm *QueueManager) Load() error {
	qm.waitGroup.Add(1)
	defer qm.waitGroup.Done()

	collection := qm.collection

	changeStream, err := collection.Watch(nil, mongo.Pipeline{}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return err
	}
	qm.changeStream = changeStream

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

	go func() {
		for qm.changeStream.Next(nil) {
			var event changeEvent[jobs.Job]
			decodeErr := qm.changeStream.Decode(&event)
			if decodeErr != nil {
				log.Error("Failed to decode change stream: ", decodeErr)
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

	return nil
}

func (qm *QueueManager) AddJob(job *jobs.Job) error {
	qm.jobQueue.Add(job)
	_, err := qm.collection.InsertOne(context.Background(), job)
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) JobInProgress(jobId int64) error {
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

func (qm *QueueManager) GetJobsCount() map[string]int {
	return qm.jobQueue.GetJobsCount()
}
