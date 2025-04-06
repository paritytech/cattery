package config

import (
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"strings"
)

var AppConfig = &CatteryConfig{}

type CatteryConfig struct {
	Server    ServerConfig
	Github    []*GitHubOrganization
	Providers []*ProviderConfig
	TrayTypes []*TrayType

	githubMap    map[string]*GitHubOrganization
	providerMap  map[string]*ProviderConfig
	trayTypesMap map[string]*TrayType
}

func LoadConfig(configPath *string) (*CatteryConfig, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	if configPath == nil {
		viper.AddConfigPath("/etc/cattery/")
	} else {
		viper.SetConfigFile(*configPath)
	}

	err := viper.ReadInConfig()
	if err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("config file not found")
		} else {
			return nil, fmt.Errorf("fatal error reading config file: %w", err)
		}
	}

	var appConfig = &CatteryConfig{}

	err = viper.Unmarshal(appConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file: %w", err)
	}

	appConfig.githubMap = make(map[string]*GitHubOrganization)
	for _, organization := range appConfig.Github {
		appConfig.githubMap[organization.Name] = organization
	}

	appConfig.providerMap = make(map[string]*ProviderConfig)
	for _, provider := range appConfig.Providers {
		appConfig.providerMap[provider.Get("name")] = provider
	}

	appConfig.trayTypesMap = make(map[string]*TrayType)
	for _, trayType := range appConfig.TrayTypes {
		appConfig.trayTypesMap[trayType.Name] = trayType
	}

	AppConfig = appConfig
	return appConfig, nil
}

// GetGitHubOrg returns the GitHub organization by name
func (c *CatteryConfig) GetGitHubOrg(name string) *GitHubOrganization {
	org, ok := c.githubMap[name]
	if !ok {
		return nil
	}
	return org
}

// GetProvider returns the provider by name
func (c *CatteryConfig) GetProvider(name string) *ProviderConfig {
	provider, ok := c.providerMap[name]
	if !ok {
		return nil
	}
	return provider
}

// GetTrayType returns the tray type by name
func (c *CatteryConfig) GetTrayType(name string) *TrayType {
	trayType, ok := c.trayTypesMap[name]
	if !ok {
		return nil
	}
	return trayType
}

type ServerConfig struct {
	ListenAddress string
	AdvertiseUrl  string
}

type GitHubOrganization struct {
	Name           string
	AppId          int64
	InstallationId int64
	WebhookSecret  string
	PrivateKeyPath string
}

type TrayType struct {
	Name          string
	Provider      string
	RunnerGroupId int64
	Shutdown      bool
	GitHubOrg     string
	Config        TrayConfig
}

type TrayConfig map[string]string

func (t TrayConfig) Get(key string) string {
	return t[strings.ToLower(key)]
}

type ProviderConfig map[string]string

func (p ProviderConfig) Get(key string) string {
	return p[strings.ToLower(key)]
}
