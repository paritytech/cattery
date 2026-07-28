package githubClient

import (
	"cattery/lib/config"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v84/github"
)

const githubAPITimeout = 30 * time.Second

var (
	githubClientsMu sync.Mutex
	githubClients   = make(map[string]*github.Client)
)

type GithubClient struct {
	client *github.Client
	Org    *config.GitHubOrganization
}

// NewGithubClient wraps an already-constructed go-github client. Tests use it
// to point the client at a fake API server; production code should use
// NewGithubClientWithOrgName.
func NewGithubClient(client *github.Client, org *config.GitHubOrganization) *GithubClient {
	return &GithubClient{
		client: client,
		Org:    org,
	}
}

func NewGithubClientWithOrgName(orgName string) (*GithubClient, error) {

	orgConfig := config.Get().GetGitHubOrg(orgName)
	if orgConfig == nil {
		return nil, errors.New("GitHub organization not found")
	}

	client, err := createClient(orgConfig)
	if err != nil {
		return nil, err
	}

	return &GithubClient{
		client: client,
		Org:    orgConfig,
	}, nil
}

func (gc *GithubClient) RestartFailedJobs(repoName string, workflowId int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	_, err := gc.client.Actions.RerunFailedJobsByID(ctx, gc.Org.Name, repoName, workflowId)
	return err
}

type WorkflowRunInfo struct {
	Status     string
	Conclusion string
	HeadBranch string
	CreatedAt  time.Time
}

func (gc *GithubClient) GetWorkflowRunInfo(repoName string, workflowRunId int64) (WorkflowRunInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	wr, _, err := gc.client.Actions.GetWorkflowRunByID(ctx, gc.Org.Name, repoName, workflowRunId)
	if err != nil {
		return WorkflowRunInfo{}, err
	}
	return WorkflowRunInfo{
		Status:     wr.GetStatus(),
		Conclusion: wr.GetConclusion(),
		HeadBranch: wr.GetHeadBranch(),
		CreatedAt:  wr.GetCreatedAt().Time,
	}, nil
}

// HasClosedPullRequestForBranch reports whether a pull request with the given
// head branch was closed (merged or not) after the given time, while no pull
// request for that branch is currently open. Detection is by head branch
// rather than commit SHA because squash/rebase merges never put the run's head
// SHA on the default branch.
func (gc *GithubClient) HasClosedPullRequestForBranch(repoName string, headBranch string, closedAfter time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	prs, _, err := gc.client.PullRequests.List(ctx, gc.Org.Name, repoName, &github.PullRequestListOptions{
		State: "all",
		Head:  gc.Org.Name + ":" + headBranch,
	})
	if err != nil {
		return false, err
	}

	for _, pr := range prs {
		if pr.GetState() == "open" {
			return false, nil
		}
		if pr.ClosedAt != nil && pr.ClosedAt.Time.After(closedAfter) {
			return true, nil
		}
	}
	return false, nil
}

// IsForbidden reports whether err is a GitHub API 403 response, which for an
// installation token means the App lacks the required permission.
func IsForbidden(err error) bool {
	var ghErr *github.ErrorResponse
	return errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusForbidden
}

// createClient creates a new GitHub client
func createClient(org *config.GitHubOrganization) (*github.Client, error) {
	githubClientsMu.Lock()
	defer githubClientsMu.Unlock()

	if githubClient, ok := githubClients[org.Name]; ok {
		return githubClient, nil
	}

	tr := http.DefaultTransport

	itr, err := ghinstallation.NewKeyFromFile(
		tr,
		org.AppId,
		org.InstallationId,
		org.PrivateKeyPath,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to load GitHub App private key for org %s: %w", org.Name, err)
	}

	// Use installation transport with github.com/google/go-github
	client := github.NewClient(&http.Client{Transport: itr})

	githubClients[org.Name] = client

	return client, nil
}
