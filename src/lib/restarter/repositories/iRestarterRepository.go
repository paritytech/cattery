package repositories

import "time"

type RestartRequest struct {
	WorkflowRunId int64     `bson:"workflowRunId"`
	OrgName       string    `bson:"orgName"`
	RepoName      string    `bson:"repoName"`
	CreatedAt     time.Time `bson:"createdAt"`
}

type IRestarterRepository interface {
	SaveRestartRequest(workflowRunId int64, orgName string, repoName string) error
	DeleteRestartRequest(workflowRunId int64) error
	CheckRestartRequest(workflowRunId int64) (bool, error)
	GetAllPendingRestartRequests() ([]RestartRequest, error)
}
