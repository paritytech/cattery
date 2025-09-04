package repositories

type IRestarterRepository interface {
	SaveRestartRequest(workflowRunId int64) error
	DeleteRestartRequest(workflowRunId int64) error
	CheckRestartRequest(workflowRunId int64) (bool, error)
}
