package githubClient

import (
	"cattery/lib/config"
	"context"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v70/github"
	log "github.com/sirupsen/logrus"
	"net/http"
)

var githubClient *github.Client = nil

type GithubClient struct {
	client *github.Client
	Org    *config.GitHubOrganization
}

func NewGithubClient(org *config.GitHubOrganization) *GithubClient {
	return &GithubClient{
		client: createClient(org),
		Org:    org,
	}
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
	// TODO: handle not existing runner
	return err
}

// createClient creates a new GitHub client
func createClient(org *config.GitHubOrganization) *github.Client {

	if githubClient != nil {
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

	githubClient = client
	return client
}
