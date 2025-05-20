package reposiroties

import (
	"cattery/lib/jobs"
)

type IJobRepository interface {
	Get(jobId int64) (*jobs.Job, error)
	Save(job *jobs.Job) error
	Delete(jobId int64) error
	GetGroupByLabels() map[string][]*jobs.Job
	Len() int
}
