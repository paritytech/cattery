package catteryClient

import (
	"bytes"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
)

// Default retry policy. Tunable via fields on CatteryClient for tests.
const (
	defaultMaxAttempts = 10
	defaultRetryDelay  = 3 * time.Second
)

type CatteryClient struct {
	httpClient *http.Client
	baseURL    string
	logger     *logrus.Entry
	agentId    string

	// maxAttempts and retryDelay control the retry policy applied to every
	// request. Transient failures (network errors, 5xx, 404) retry up to
	// maxAttempts times with retryDelay between each. Permanent client
	// errors (other 4xx) fail fast.
	maxAttempts int
	retryDelay  time.Duration
}

func NewCatteryClient(baseURL string, agentId string) *CatteryClient {
	return &CatteryClient{
		httpClient:  &http.Client{},
		baseURL:     baseURL,
		logger:      logrus.WithField("name", "catteryClient"),
		agentId:     agentId,
		maxAttempts: defaultMaxAttempts,
		retryDelay:  defaultRetryDelay,
	}
}

// RegisterAgent requests just-in-time runner configuration from the Cattery
// server. 404 retries cover the race where the agent boots before the server
// has finished persisting the tray row.
//
// https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-an-organization
func (c *CatteryClient) RegisterAgent(id string) (*agents.Agent, *string, error) {
	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "register/", id)
	if err != nil {
		return nil, nil, err
	}

	var resp messages.RegisterResponse
	if err := c.doRequest("GET", requestUrl, nil, &resp); err != nil {
		return nil, nil, err
	}
	return &resp.Agent, &resp.JitConfig, nil
}

// UnregisterAgent tells the server the agent is shutting down.
func (c *CatteryClient) UnregisterAgent(agent *agents.Agent, reason messages.UnregisterReason, message string) error {
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

	return c.doRequest("POST", requestUrl, requestJson, nil)
}

func (c *CatteryClient) Ping() (*messages.PingResponse, error) {
	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "ping", c.agentId)
	if err != nil {
		return nil, fmt.Errorf("failed to join path: %w", err)
	}

	pingResponse := &messages.PingResponse{}
	if err := c.doRequest("POST", requestUrl, nil, pingResponse); err != nil {
		return nil, err
	}
	return pingResponse, nil
}

// doRequest sends an HTTP request and decodes a 200 response body into dest
// (if non-nil), retrying transient failures.
//
// Retryable conditions: network errors, 5xx responses, and 404 — which can
// indicate a race against tray-row creation rather than a true "not found."
// Permanent client errors (other 4xx) fail immediately.
func (c *CatteryClient) doRequest(method, requestUrl string, body []byte, dest any) error {
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		err, retryable := c.try(method, requestUrl, body, dest)
		if err == nil {
			return nil
		}
		if !retryable {
			return err
		}
		lastErr = err
		if attempt < c.maxAttempts {
			c.logger.Warnf("%s %s attempt %d/%d failed (retrying in %s): %v", method, requestUrl, attempt, c.maxAttempts, c.retryDelay, err)
			time.Sleep(c.retryDelay)
		}
	}
	return fmt.Errorf("%s %s failed after %d attempts: %w", method, requestUrl, c.maxAttempts, lastErr)
}

// try performs a single request. The retryable flag is true when the failure
// looks transient: network errors at any stage (including a connection drop
// mid-body), 5xx responses, and 404. Permanent client errors (other 4xx) and
// programming errors (bad URL/method, malformed JSON) are not retryable.
func (c *CatteryClient) try(method, requestUrl string, body []byte, dest any) (error, bool) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, requestUrl, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err), false
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err, true
	}
	defer response.Body.Close()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err), true
	}

	if response.StatusCode == http.StatusOK {
		if dest != nil {
			if err := json.Unmarshal(bodyBytes, dest); err != nil {
				return fmt.Errorf("decode response body: %w", err), false
			}
		}
		return nil, false
	}

	httpErr := fmt.Errorf("response status code: %s body: %s", response.Status, string(bodyBytes))
	retryable := response.StatusCode == http.StatusNotFound || response.StatusCode >= 500
	return httpErr, retryable
}
