package jobQueue

import (
	"cattery/lib/githubClient"
	"cattery/lib/jobs"
	"cattery/lib/metrics"
	"context"
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type QueueManager struct {
	jobQueue  *JobQueue
	waitGroup sync.WaitGroup

	collection   *mongo.Collection
	changeStream *mongo.ChangeStream

	logger *log.Entry
}

func NewQueueManager() *QueueManager {
	return &QueueManager{
		jobQueue:  NewJobQueue(),
		waitGroup: sync.WaitGroup{},
		logger:    log.WithFields(log.Fields{"name": "QueueManager"}),
	}
}

func (qm *QueueManager) Connect(collection *mongo.Collection) {
	qm.collection = collection
}

func (qm *QueueManager) Load() error {
	qm.waitGroup.Add(1)
	defer qm.waitGroup.Done()

	collection := qm.collection

	ctx, _ := context.WithCancel(context.Background())

	changeStream, err := collection.Watch(ctx, mongo.Pipeline{}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return err
	}
	qm.changeStream = changeStream
	//options.ChangeStream().SetFullDocumentBeforeChange(options.UpdateLookup)
	allJobs, err := collection.Find(context.Background(), bson.M{})
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
		for qm.changeStream.Next(ctx) {
			qm.logger.Debug("changeStream event")
			var event changeEvent[jobs.Job]
			decodeErr := qm.changeStream.Decode(&event)
			if decodeErr != nil {
				qm.logger.Error("Failed to decode change stream: ", decodeErr)
				continue
			}

			switch event.OperationType {
			case "replace":
				fallthrough
			case "update":
				fallthrough
			case "insert":
				qm.jobQueue.Add(&event.FullDocument)
				qm.logger.Debug("Inserted object from changeStream: ", event.FullDocument)
			case "delete":
				qm.jobQueue.Delete(event.DocumentKey.Id)
				qm.logger.Debug("Deleted object from changeStream: ", event.DocumentKey.Id)
			default:
				qm.logger.Warn("Unknown operation type: ", event.OperationType)
			}
		}
		qm.logger.Debug("changeStream finished")
		if err := qm.changeStream.Err(); err != nil {
			qm.logger.Error("changeStream error: ", err)
		}
		changeStream.Close(nil)
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
	//TODO: remove method, use UpdateJobStatus
	job := qm.jobQueue.Get(jobId)
	if job == nil {
		qm.logger.Errorf("No job found with id %v", jobId)
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
		return nil
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
	_, err := qm.collection.DeleteOne(context.Background(), bson.M{"_id": jobId})
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) GetJobsCount() map[string]int {
	return qm.jobQueue.GetJobsCount()
}

func (qm *QueueManager) CleanupByWorkflowRun(workflowRunId int64) error {
	qm.jobQueue.DeleteJobsByWorkflowRunId(workflowRunId)
	_, err := qm.collection.DeleteMany(context.Background(), bson.M{"workflowRunId": workflowRunId})
	if err != nil {
		return err
	}

	return nil
}

func (qm *QueueManager) CleanupCompletedJobs() error {

	for _, job := range qm.jobQueue.jobs {
		var ghClient, err = githubClient.NewGithubClientWithOrgName(job.Organization)
		if err != nil {
			return err
		}

		completed, err := ghClient.CheckJobCompleted(job.Repository, job.Id)
		if err != nil {
			return err
		}

		if completed {
			qm.logger.Warn("Removed completed job: ", job.Id)
			qm.deleteJob(job.Id)

			metrics.StaleJobsInc(job.Organization, job.Repository, job.Name, job.TrayType)
		}
	}

	return nil
}
