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
