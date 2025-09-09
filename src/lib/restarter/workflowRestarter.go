package restarter

import (
	"cattery/lib/githubClient"
	"cattery/lib/restarter/repositories"

	log "github.com/sirupsen/logrus"
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

func (wr *WorkflowRestarter) Restart(workflowRunId int64, ghOrg string, repoName string) error {

	// check that workflow is in db
	log.Debugf("Checking restart request for workflow run id %d", workflowRunId)
	exists, err := wr.repository.CheckRestartRequest(workflowRunId)
	if err != nil {
		log.Errorf("Failed to check restart request: %s", err.Error())
		return err
	}
	if !exists {
		log.Debugf("No restart request found for workflow run id %d", workflowRunId)
		return nil
	}
	ghClient, err := githubClient.NewGithubClientWithOrgName(ghOrg)
	if err != nil {
		log.Errorf("Failed to get GitHub client: %s", err.Error())
	}
	err = ghClient.RestartFailedJobs(repoName, workflowRunId)
	if err != nil {
		log.Errorf("Failed to restart workflow run id %d: %v", workflowRunId, err)
		return err
	}
	err = wr.repository.DeleteRestartRequest(workflowRunId)
	if err != nil {
		log.Errorf("Failed to delete restart request for workflow run id %d: %v", workflowRunId, err)
		return err
	}
	return nil
}
