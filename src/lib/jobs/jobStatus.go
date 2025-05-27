package jobs

type JobStatus int

const (
	JobStatusQueued JobStatus = iota
	JobStatusInProgress
	JobStatusFinished
)

var stateName = map[JobStatus]string{
	JobStatusQueued:     "queued",
	JobStatusInProgress: "in_progress",
	JobStatusFinished:   "finished",
}

func (js JobStatus) String() string {
	return stateName[js]
}
