package jobs

import "github.com/google/go-github/v70/github"

type Job struct {
	Id           int64    `bson:"id"`
	Action       string   `bson:"action"`
	WorkflowId   int64    `bson:"workflowId"`
	Repository   string   `bson:"repository"`
	Organization string   `bson:"organization"`
	Labels       []string `bson:"labels"`
}

func FromGithubModel(workflowJobEvent *github.WorkflowJobEvent) *Job {
	return &Job{
		Id:           workflowJobEvent.GetWorkflowJob().GetID(),
		Action:       workflowJobEvent.GetAction(),
		WorkflowId:   workflowJobEvent.GetWorkflowJob().GetRunID(),
		Repository:   workflowJobEvent.GetRepo().GetName(),
		Organization: workflowJobEvent.GetOrg().GetLogin(),
		Labels:       workflowJobEvent.GetWorkflowJob().Labels,
	}
}
