package agent

import (
	"bytes"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"encoding/json"
	"errors"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"net/url"
)

type CatteryClient struct {
	httpClient *http.Client
	baseURL    string
	logger     *logrus.Entry
}

func NewCatteryClient(baseURL string) *CatteryClient {
	return &CatteryClient{
		httpClient: &http.Client{},
		baseURL:    baseURL,
		logger:     logrus.WithField("name", "catteryClient"),
	}
}

// RegisterAgent request just-in-time runner configuration from the Cattery server
// and returns the configuration as a base64 encoded string
//
// https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-an-organization
func (c *CatteryClient) RegisterAgent(id string) (*agents.Agent, *string, error) {

	var client = c.httpClient

	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "register/", id)
	if err != nil {
		return nil, nil, err
	}

	var request, _ = http.NewRequest("GET", requestUrl, nil)
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}

	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body)
		return nil, nil, errors.New("response status code: " + response.Status + " body: " + string(bodyBytes))
	}

	var registerResponse = &messages.RegisterResponse{}
	err = json.NewDecoder(response.Body).Decode(registerResponse)
	if err != nil {
		return nil, nil, err
	}

	return &registerResponse.Agent, &registerResponse.JitConfig, nil
}

// UnregisterAgent sends a POST request to the Cattery server to unregister the agent
func (c *CatteryClient) UnregisterAgent(agent *agents.Agent, reason messages.UnregisterReason) error {

	var client = c.httpClient

	requestJson, err := json.Marshal(messages.UnregisterRequest{
		Agent:  *agent,
		Reason: reason,
	})
	if err != nil {
		return err
	}

	requestUrl, err := url.JoinPath(c.baseURL, "/agent", "unregister/", agent.AgentId)
	if err != nil {
		return err
	}

	var request, _ = http.NewRequest("POST", requestUrl, bytes.NewBuffer(requestJson))
	response, err := client.Do(request)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body)
		return errors.New("response status code: " + response.Status + " body: " + string(bodyBytes))
	}

	return nil
}
