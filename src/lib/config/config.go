package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

var AppConfig = &CatteryConfig{}

type CatteryConfig struct {
	Server    ServerConfig          `yaml:"server" validate:"required"`
	Database  DatabaseConfig        `yaml:"database" validate:"required"`
	Github    []*GitHubOrganization `yaml:"github" validate:"required,dive,required"`
	Providers []*ProviderConfig     `yaml:"providers" validate:"required,dive,required"`
	TrayTypes []*TrayType           `yaml:"trayTypes" validate:"required,dive,required"`

	githubMap    map[string]*GitHubOrganization
	providerMap  map[string]*ProviderConfig
	trayTypesMap map[string]*TrayType
}

func LoadConfig(configPath *string) (*CatteryConfig, error) {

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	if *configPath == "" {
		viper.AddConfigPath("/etc/cattery/")
		viper.AddConfigPath("./")
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

		var providerConfig, ok = appConfig.providerMap[trayType.Provider]

		if !ok {
			return nil, fmt.Errorf("provider %s for trayType %s not found", trayType.Provider, trayType.Name)
		}

		switch providerConfig.Get("type") {
		case "google":
			var gc GoogleTrayConfig
			if err := mapstructure.Decode(trayType.Config, &gc); err != nil {
				return nil, fmt.Errorf("failed to unmarshal google: %w", err)
			}
			trayType.Config = &gc
		case "docker":
			var dc DockerTrayConfig
			if err := mapstructure.Decode(trayType.Config, &dc); err != nil {
				return nil, fmt.Errorf("failed to unmarshal docker: %w", err)
			}
			trayType.Config = &dc
		//case "scaleway":
		default:

		}
	}

	AppConfig = appConfig

	validate := validator.New()
	err = validate.Struct(AppConfig)
	if err != nil {
		// err is of type validator.ValidationErrors
		for _, fieldErr := range err.(validator.ValidationErrors) {
			return nil, fmt.Errorf("Validation failed on field '%s' for tag '%s'\n", fieldErr.Namespace(), fieldErr.Tag())
		}
	}

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
	ListenAddress string `yaml:"listenAddress" validate:"required"`
	AdvertiseUrl  string `yaml:"advertiseUrl" validate:"required"`
}

type DatabaseConfig struct {
	Uri      string `yaml:"uri" validate:"required"`
	Database string `yaml:"database" validate:"required"`
}

type GitHubOrganization struct {
	Name           string `yaml:"name" validate:"required"`
	AppId          int64  `yaml:"appId" validate:"required"`
	InstallationId int64  `yaml:"installationId" validate:"required"`
	WebhookSecret  string `yaml:"webhookSecret"`
	PrivateKeyPath string `yaml:"privateKeyPath"`
}

type TrayType struct {
	Name          string     `yaml:"name" validate:"required"`
	Provider      string     `yaml:"provider" validate:"required"`
	RunnerGroupId int64      `yaml:"runnerGroupId" validate:"required"`
	Shutdown      bool       `yaml:"shutdown"`
	GitHubOrg     string     `yaml:"githubOrg" validate:"required"`
	MaxTrays      int        `yaml:"limit"`
	Config        TrayConfig `yaml:"config"`
	ExtraMetadata TrayExtraMetadata
}

type TrayExtraMetadata map[string]string

type ProviderConfig map[string]string

func (p ProviderConfig) Get(key string) string {
	return p[strings.ToLower(key)]
}
