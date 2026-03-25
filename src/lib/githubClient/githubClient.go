package githubClient

import (
	"cattery/lib/config"
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
)

var (
	githubClientsMu sync.Mutex
	githubClients   = make(map[string]*github.Client)
)

type GithubClient struct {
	client *github.Client
	Org    *config.GitHubOrganization
}

func NewGithubClientWithOrgConfig(org *config.GitHubOrganization) *GithubClient {
	return &GithubClient{
		client: createClient(org),
		Org:    org,
	}
}

func NewGithubClientWithOrgName(orgName string) (*GithubClient, error) {

	var orgConfig = config.AppConfig.GetGitHubOrg(orgName)
	if orgConfig == nil {
		return nil, errors.New("GitHub organization not found")
	}

	return &GithubClient{
		client: createClient(orgConfig),
		Org:    orgConfig,
	}, nil
}

// CreateJITConfig creates a new JIT config
func (gc *GithubClient) CreateJITConfig(name string, runnerGroupId int64, labels []string) (*github.JITRunnerConfig, error) {
	jitConfig, _, err := gc.client.Actions.GenerateOrgJITConfig(
		context.Background(),
		gc.Org.Name,
		&github.GenerateJITConfigRequest{
			Name:          name,
			RunnerGroupID: runnerGroupId,
			Labels:        labels,
		},
	)

	return jitConfig, err
}

func (gc *GithubClient) RemoveRunner(runnerId int64) error {
	_, err := gc.client.Actions.RemoveOrganizationRunner(context.Background(), gc.Org.Name, runnerId)
	return err
}

func (gc *GithubClient) RestartFailedJobs(repoName string, workflowId int64) error {
	wr, _, err := gc.client.Actions.GetWorkflowRunByID(context.Background(), gc.Org.Name, repoName, workflowId)
	if err != nil {
		log.Errorf("Failed to get workflow run by id %d: %v", workflowId, err)
		return err
	}
	log.Debugf("Workflow run status: %s, conclusion: %s", wr.GetStatus(), wr.GetConclusion())
	_, err = gc.client.Actions.RerunFailedJobsByID(context.Background(), gc.Org.Name, repoName, workflowId)
	return err
}

func (gc *GithubClient) GetWorkflowRunStatus(repoName string, workflowRunId int64) (string, string, error) {
	wr, _, err := gc.client.Actions.GetWorkflowRunByID(context.Background(), gc.Org.Name, repoName, workflowRunId)
	if err != nil {
		return "", "", err
	}
	return wr.GetStatus(), wr.GetConclusion(), nil
}

func (gc *GithubClient) CheckJobCompleted(repoName string, jobId int64) (bool, error) {
	wfJob, resp, err := gc.client.Actions.GetWorkflowJobByID(context.Background(), gc.Org.Name, repoName, jobId)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			log.Tracef("Workflow job not found: %s/%s %d", gc.Org.Name, repoName, jobId)
			return true, nil
		}
		return false, err
	}

	var status = wfJob.GetStatus()

	return status == "completed", nil
}

// createClient creates a new GitHub client
func createClient(org *config.GitHubOrganization) *github.Client {
	githubClientsMu.Lock()
	defer githubClientsMu.Unlock()

	if githubClient, ok := githubClients[org.Name]; ok {
		return githubClient
	}

	tr := http.DefaultTransport

	itr, err := ghinstallation.NewKeyFromFile(
		tr,
		org.AppId,
		org.InstallationId,
		org.PrivateKeyPath,
	)

	if err != nil {
		log.Fatal(err)
	}

	// Use installation transport with github.com/google/go-github
	client := github.NewClient(&http.Client{Transport: itr})

	githubClients[org.Name] = client

	return client
}
