package githubClient

import (
	"cattery/lib/config"
	"context"
	"errors"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
)

var githubClients = make(map[string]*github.Client)

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

// createClient creates a new GitHub client
func createClient(org *config.GitHubOrganization) *github.Client {

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

// func
