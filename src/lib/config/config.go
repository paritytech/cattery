package config

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-playground/validator"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

var appConfig atomic.Pointer[CatteryConfig]

func init() {
	appConfig.Store(&CatteryConfig{})
}

// Get returns the current config snapshot.
func Get() *CatteryConfig {
	return appConfig.Load()
}

// Set atomically replaces the config. Used by LoadConfig and tests.
func Set(cfg *CatteryConfig) {
	appConfig.Store(cfg)
}

// SetForTest sets the config for the duration of a test and restores it on cleanup.
func SetForTest(t *testing.T, cfg *CatteryConfig) {
	cfg.InitMaps()
	old := Get()
	Set(cfg)
	t.Cleanup(func() { Set(old) })
}


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

// InitMaps builds the internal lookup maps from the slice fields.
// Called automatically by LoadConfig; call manually when constructing CatteryConfig in tests.
func (c *CatteryConfig) InitMaps() {
	c.githubMap = make(map[string]*GitHubOrganization)
	for _, org := range c.Github {
		c.githubMap[org.Name] = org
	}
	c.providerMap = make(map[string]*ProviderConfig)
	for _, p := range c.Providers {
		c.providerMap[p.Get("name")] = p
	}
	c.trayTypesMap = make(map[string]*TrayType)
	for _, tt := range c.TrayTypes {
		c.trayTypesMap[tt.Name] = tt
	}
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

	cfg := &CatteryConfig{}

	err = viper.Unmarshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file: %w", err)
	}

	switch cfg.Database.Type {
	case "":
		return nil, fmt.Errorf("database.type is required (supported: sqlite, mongodb)")
	case "mongodb":
		if cfg.Database.Uri == "" {
			return nil, fmt.Errorf("database.uri is required for mongodb")
		}
		if cfg.Database.Database == "" {
			return nil, fmt.Errorf("database.database is required for mongodb")
		}
	case "sqlite":
		if cfg.Database.Path == "" {
			return nil, fmt.Errorf("database.path is required for sqlite")
		}
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Database.Type)
	}

	cfg.githubMap = make(map[string]*GitHubOrganization)
	for _, organization := range cfg.Github {
		cfg.githubMap[organization.Name] = organization
	}

	cfg.providerMap = make(map[string]*ProviderConfig)
	for _, provider := range cfg.Providers {
		cfg.providerMap[provider.Get("name")] = provider
	}

	cfg.trayTypesMap = make(map[string]*TrayType)
	for _, trayType := range cfg.TrayTypes {
		cfg.trayTypesMap[trayType.Name] = trayType

		providerConfig, ok := cfg.providerMap[trayType.Provider]

		if !ok {
			return nil, fmt.Errorf("provider %s for trayType %s not found", trayType.Provider, trayType.Name)
		}

		var decodeError error
		switch providerConfig.Get("type") {
		case "google":
			var gc GoogleTrayConfig
			decodeError = mapstructure.Decode(trayType.Config, &gc)
			trayType.Config = gc
		case "docker":
			var dc DockerTrayConfig
			decodeError = mapstructure.Decode(trayType.Config, &dc)
			trayType.Config = dc
		//case "scaleway":
		default:

		}

		if decodeError != nil {
			return nil, fmt.Errorf("failed to decode '%s' %w", providerConfig.Get("type"), decodeError)
		}
	}

	validate := validator.New()
	err = validate.Struct(cfg)
	if err != nil {
		// err is of type validator.ValidationErrors
		for _, fieldErr := range err.(validator.ValidationErrors) {
			return nil, fmt.Errorf("Validation failed on field '%s' for tag '%s'\n", fieldErr.Namespace(), fieldErr.Tag())
		}
	}

	Set(cfg)

	return cfg, nil
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
	// StatusListenAddress is the address for the /status and /metrics endpoints.
	// If empty or equal to ListenAddress, these routes are served on the agent port.
	StatusListenAddress string `yaml:"statusListenAddress"`
	AdvertiseUrl        string `yaml:"advertiseUrl" validate:"required"`
	AgentSecret         string `yaml:"agentSecret"`
}

type DatabaseConfig struct {
	Type     string `yaml:"type"` // "mongodb" (default) or "sqlite"
	Uri      string `yaml:"uri"`
	Database string `yaml:"database"`
	Path     string `yaml:"path"` // SQLite file path
}

type GitHubOrganization struct {
	Name           string `yaml:"name" validate:"required"`
	AppId          int64  `yaml:"appId" validate:"required"`
	AppClientId    string `yaml:"appClientId" validate:"required"`
	InstallationId int64  `yaml:"installationId" validate:"required"`
	PrivateKeyPath string `yaml:"privateKeyPath"`
}

const DefaultMaxParallelCreation = 10

type TrayType struct {
	Name                string     `yaml:"name" validate:"required"`
	Provider            string     `yaml:"provider" validate:"required"`
	RunnerGroupId       int64      `yaml:"runnerGroupId" validate:"required"`
	Shutdown            bool       `yaml:"shutdown"`
	GitHubOrg           string     `yaml:"githubOrg" validate:"required"`
	MaxTrays            int        `yaml:"maxTrays"`
	MaxParallelCreation int        `yaml:"maxParallelCreation"`
	Config              TrayConfig `yaml:"config"`
	ExtraMetadata       TrayExtraMetadata
}

type TrayExtraMetadata map[string]string

type ProviderConfig map[string]string

func (p ProviderConfig) Get(key string) string {
	return p[strings.ToLower(key)]
}
