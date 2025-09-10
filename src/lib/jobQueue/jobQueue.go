package jobQueue

import (
	"cattery/lib/jobs"
	"sync"
)

type JobQueue struct {
	rwMutex *sync.RWMutex
	jobs    map[int64]jobs.Job
	groups  map[string]map[int64]jobs.Job
}

func NewJobQueue() *JobQueue {
	return &JobQueue{
		rwMutex: &sync.RWMutex{},
		jobs:    make(map[int64]jobs.Job),
		groups:  make(map[string]map[int64]jobs.Job),
	}
}

func (qm *JobQueue) GetGroup(groupName string) map[int64]jobs.Job {
	qm.rwMutex.RLock()
	defer qm.rwMutex.RUnlock()

	return qm.getGroup(groupName)
}

func (qm *JobQueue) getGroup(groupName string) map[int64]jobs.Job {
	if group, ok := qm.groups[groupName]; ok {
		return group
	}

	newGroup := make(map[int64]jobs.Job)
	qm.groups[groupName] = newGroup
	return newGroup
}

func (qm *JobQueue) GetJobsCount() map[string]int {
	result := make(map[string]int)
	qm.rwMutex.RLock()
	defer qm.rwMutex.RUnlock()
	for groupName, group := range qm.groups {
		result[groupName] = len(group)
	}
	return result
}

func (qm *JobQueue) Get(jobId int64) *jobs.Job {
	qm.rwMutex.RLock()
	defer qm.rwMutex.RUnlock()

	if job, ok := qm.jobs[jobId]; ok {
		return &job
	}

	return nil
}

func (qm *JobQueue) Add(job *jobs.Job) {
	qm.rwMutex.Lock()
	defer qm.rwMutex.Unlock()

	if _, exists := qm.jobs[job.Id]; exists {
		// TODO: handle error or return
		return // Job already exists
	}

	qm.jobs[job.Id] = *job

	var group = qm.getGroup(job.TrayType)
	group[job.Id] = *job
}

func (qm *JobQueue) Delete(jobId int64) {
	qm.rwMutex.Lock()
	defer qm.rwMutex.Unlock()

	if job, exists := qm.jobs[jobId]; exists {

		delete(qm.jobs, jobId)

		var group = qm.getGroup(job.TrayType)
		delete(group, job.Id)
	}

}

func (qm *JobQueue) GetByWorkflowRunId(workflowRunId int64) []*jobs.Job {
	qm.rwMutex.RLock()
	defer qm.rwMutex.RUnlock()

	var result []*jobs.Job
	for _, job := range qm.jobs {
		if job.WorkflowId == workflowRunId {
			result = append(result, &job)
		}
	}

	return result
}
