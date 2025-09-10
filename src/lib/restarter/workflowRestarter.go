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
	log.Debugf("Requesting restart for workflow run id %d", workflowRunId)
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
	log.Debugf("Restarting failed jobs for workflow run id %d", workflowRunId)
	err = ghClient.RestartFailedJobs(repoName, workflowRunId)
	if err != nil {
		log.Errorf("Failed to restart workflow run id %d: %v", workflowRunId, err)
		return err
	}
	log.Debugf("Successfully restarted failed jobs for workflow run id %d, removing restart request from DB", workflowRunId)
	err = wr.repository.DeleteRestartRequest(workflowRunId)
	if err != nil {
		log.Errorf("Failed to delete restart request for workflow run id %d: %v", workflowRunId, err)
		return err
	}
	log.Debugf("Finished restart request for workflow run id %d", workflowRunId)
	return nil
}

// cleanup db on cancelled or completed workflow runs
func (wr *WorkflowRestarter) Cleanup(workflowRunId int64, ghOrg string, repoName string) error {
	log.Debugf("Cleanup for workflow run id %d", workflowRunId)
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
	log.Debugf("Successfully cleaned up restart request for workflow run id %d, removing restart request from DB", workflowRunId)
	err = wr.repository.DeleteRestartRequest(workflowRunId)
	if err != nil {
		log.Errorf("Failed to delete restart request for workflow run id %d: %v", workflowRunId, err)
		return err
	}
	log.Debugf("Finished cleanup restart request for workflow run id %d", workflowRunId)
	return nil
}
