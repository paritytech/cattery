package repositories

import (
	"context"
	"time"
)

type RestartRequest struct {
	WorkflowRunId int64     `bson:"workflowRunId"`
	OrgName       string    `bson:"orgName"`
	RepoName      string    `bson:"repoName"`
	CreatedAt     time.Time `bson:"createdAt"`
}

type RestarterRepository interface {
	SaveRestartRequest(ctx context.Context, workflowRunId int64, orgName string, repoName string) error
	DeleteRestartRequest(ctx context.Context, workflowRunId int64) error
	GetAllPendingRestartRequests(ctx context.Context) ([]RestartRequest, error)
}
