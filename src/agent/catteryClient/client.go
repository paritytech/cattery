package catteryClient

import (
	"bytes"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
)

// Per-request timeout applied when the caller supplies a context without a
// deadline. Keeps a dead or unreachable server from wedging the agent.
const defaultRequestTimeout = 30 * time.Second

type CatteryClient struct {
	httpClient *http.Client
	baseURL    string
	logger     *logrus.Entry
	agentId    string
}

func NewCatteryClient(baseURL string, agentId string) *CatteryClient {
	return &CatteryClient{
		httpClient: &http.Client{Timeout: defaultRequestTimeout},
		baseURL:    baseURL,
		logger:     logrus.WithField("name", "catteryClient"),
		agentId:    agentId,
	}
}

// RegisterAgent requests just-in-time runner configuration from the Cattery
// server and returns the agent plus its JIT config blob.
//
// https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-an-organization
func (c *CatteryClient) RegisterAgent(ctx context.Context, id string) (*agents.Agent, *string, error) {
	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "register/", id)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create register request: %w", err)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("register request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body)
		return nil, nil, fmt.Errorf("register response status %s body: %s", response.Status, string(bodyBytes))
	}

	registerResponse := &messages.RegisterResponse{}
	if err := json.NewDecoder(response.Body).Decode(registerResponse); err != nil {
		return nil, nil, fmt.Errorf("decode register response: %w", err)
	}

	return &registerResponse.Agent, &registerResponse.JitConfig, nil
}

// UnregisterAgent tells the server to unregister this agent.
func (c *CatteryClient) UnregisterAgent(ctx context.Context, agent *agents.Agent, reason messages.UnregisterReason, message string) error {
	requestJson, err := json.Marshal(messages.UnregisterRequest{
		Agent:   *agent,
		Reason:  reason,
		Message: message,
	})
	if err != nil {
		return err
	}

	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "unregister/", agent.AgentId)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestUrl, bytes.NewBuffer(requestJson))
	if err != nil {
		return fmt.Errorf("create unregister request: %w", err)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unregister request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body)
		return fmt.Errorf("unregister response status %s body: %s", response.Status, string(bodyBytes))
	}

	return nil
}

func (c *CatteryClient) Ping(ctx context.Context) (*messages.PingResponse, error) {
	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "ping", c.agentId)
	if err != nil {
		return nil, fmt.Errorf("join path: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("create ping request: %w", err)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ping request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("ping response status %s body: %s", response.Status, string(bodyBytes))
	}

	pingResponse := &messages.PingResponse{}
	if err := json.NewDecoder(response.Body).Decode(pingResponse); err != nil {
		return nil, fmt.Errorf("decode ping response: %w", err)
	}

	return pingResponse, nil
}
