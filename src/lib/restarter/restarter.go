package restarter

import (
	"cattery/lib/githubClient"
	"cattery/lib/restarter/repositories"
	"fmt"
)

type WorkflowRestarter struct {
	repository repositories.IRestarterRepository
}

func NewWorkflowRestarter(repository repositories.IRestarterRepository) *WorkflowRestarter {
	return &WorkflowRestarter{
		repository: repository,
	}
}

func (wr *WorkflowRestarter) RequestRestart(workflowRunId int64) error {
	return wr.repository.SaveRestartRequest(workflowRunId)
}

func (wr *WorkflowRestarter) Restart(workflowRunId int64, ghOrg string) error {
	ghClient, err := githubClient.NewGithubClientWithOrgName(ghOrg)
	if err != nil {
		fmt.Errorf("Failed to get GitHub client: %s", err.Error())
	}
	_ = ghClient
	return nil
}
