package jobs

import "github.com/google/go-github/v70/github"

type Job struct {
	Id           int64            `bson:"_id"`
	Name         string           `bson:"name"`
	Action       string           `bson:"action"`
	WorkflowId   int64            `bson:"workflowId"`
	WorkflowName string           `bson:"workflowName"`
	Repository   string           `bson:"repository"`
	Organization string           `bson:"organization"`
	Labels       []string         `bson:"labels"`
	RunnerName   string           `bson:"runnerName"`
	TrayType     string           `bson:"trayType"`
	CreatedAt    github.Timestamp `bson:"createdAt"`
}

func FromGithubModel(workflowJobEvent *github.WorkflowJobEvent) *Job {
	return &Job{
		Id:           workflowJobEvent.GetWorkflowJob().GetID(),
		Name:         workflowJobEvent.GetWorkflowJob().GetName(),
		Action:       workflowJobEvent.GetAction(),
		WorkflowId:   workflowJobEvent.GetWorkflowJob().GetRunID(),
		WorkflowName: workflowJobEvent.GetWorkflowJob().GetWorkflowName(),
		Repository:   workflowJobEvent.GetRepo().GetName(),
		Organization: workflowJobEvent.GetOrg().GetLogin(),
		RunnerName:   workflowJobEvent.GetWorkflowJob().GetRunnerName(),
		Labels:       workflowJobEvent.GetWorkflowJob().Labels,
		CreatedAt:    workflowJobEvent.GetWorkflowJob().GetCreatedAt(),
	}
}
