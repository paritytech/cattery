package config

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
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
	Server       ServerConfig          `yaml:"server" validate:"required"`
	Database     DatabaseConfig        `yaml:"database" validate:"required"`
	Stale        StaleConfig           `yaml:"stale"`
	Coordination CoordinationConfig    `yaml:"coordination"`
	Github       []*GitHubOrganization `yaml:"github" validate:"required,dive,required"`
	Providers    []*ProviderConfig     `yaml:"providers" validate:"required,dive,required"`
	TrayTypes    []*TrayType           `yaml:"trayTypes" validate:"required,dive,required"`

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
		case "nomad":
			var nc NomadTrayConfig
			decodeError = mapstructure.Decode(trayType.Config, &nc)
			trayType.Config = nc
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
	Uri      string `yaml:"uri" validate:"required"`
	Database string `yaml:"database" validate:"required"`
}

// StaleConfig configures the stale-tray cleanup loop.
//
// PollInterval is how often the loop checks for stale trays.
// Thresholds maps a tray status name (lowercase: "creating", "registering",
// "registered", "deleting") to the duration after which a tray sitting in
// that status is considered stale. Statuses not present in the map are not
// checked. "running" should not be set — running trays are never stale.
//
// Defaults are applied in StaleConfig.WithDefaults when fields are zero.
type StaleConfig struct {
	PollInterval time.Duration            `yaml:"pollInterval"`
	Thresholds   map[string]time.Duration `yaml:"thresholds"`
}

// DefaultStalePollInterval is used when StaleConfig.PollInterval is zero.
const DefaultStalePollInterval = time.Minute

// DefaultStaleThresholds is used when StaleConfig.Thresholds is empty.
// Creating/Registering are tight: a VM that hasn't booted+registered within
// a few minutes is almost certainly broken. Registered is generous: a tray
// may legitimately idle waiting for a job.
var DefaultStaleThresholds = map[string]time.Duration{
	"creating":    5 * time.Minute,
	"registering": 5 * time.Minute,
	"registered":  15 * time.Minute,
	"deleting":    15 * time.Minute,
}

// WithDefaults returns a copy with zero/empty fields populated from defaults.
func (s StaleConfig) WithDefaults() StaleConfig {
	out := s
	if out.PollInterval <= 0 {
		out.PollInterval = DefaultStalePollInterval
	}
	if len(out.Thresholds) == 0 {
		out.Thresholds = DefaultStaleThresholds
	}
	return out
}

// CoordinationConfig selects the leader-election backend and tunes the lease
// cadence. Leader election decides which replica runs each tray type's scale
// set poller; every replica serves the tray HTTP plane regardless.
//
// Backend "memory" (the default) is a no-op elector that always leads — correct
// and zero-overhead for SINGLE-replica deployments. Run more than one replica
// on "memory" and every replica will try to hold the GitHub session for every
// tray type, which conflicts. To run multiple replicas, pick a shared backend
// (e.g. "mongo"), which leases each tray type to one replica at a time.
type CoordinationConfig struct {
	// Backend is the election backend: "memory" (single replica), "mongo", or
	// "k8s" (native coordination.k8s.io Leases; requires running in-cluster).
	Backend    string                `yaml:"backend" validate:"omitempty,oneof=memory mongo k8s"`
	Lease      LeaseConfig           `yaml:"lease"`
	Kubernetes KubernetesCoordConfig `yaml:"kubernetes"`
}

// KubernetesCoordConfig configures the "k8s" election backend. Both fields are
// optional. Namespace defaults to the pod's namespace (POD_NAMESPACE env, then
// the service-account namespace file, then "default"). LeaseNamePrefix is
// prepended to the sanitized tray type name to form each Lease object's name.
type KubernetesCoordConfig struct {
	Namespace       string `yaml:"namespace"`
	LeaseNamePrefix string `yaml:"leaseNamePrefix"`
}

// LeaseConfig tunes the lease-based electors. Ignored by the "memory" backend.
//
// TTL bounds worst-case failover: a dead leader's tray type is reclaimable
// after ~TTL. RenewInterval is how often the leader renews (default TTL/3 —
// keep it well below TTL so a missed renew or two does not drop leadership).
// RetryInterval is how often a non-leader retries acquisition.
type LeaseConfig struct {
	TTL           time.Duration `yaml:"ttl"`
	RenewInterval time.Duration `yaml:"renewInterval"`
	RetryInterval time.Duration `yaml:"retryInterval"`
}

const (
	CoordinationBackendMemory = "memory"
	CoordinationBackendMongo  = "mongo"
	CoordinationBackendK8s    = "k8s"

	DefaultLeaseTTL           = 30 * time.Second
	DefaultLeaseRetryInterval = 5 * time.Second

	DefaultLeaseNamePrefix = "cattery-"
)

// WithDefaults returns a copy with zero/empty fields populated from defaults:
// the "memory" backend and the standard lease cadence (TTL, TTL/3 renew, 5s retry).
func (c CoordinationConfig) WithDefaults() CoordinationConfig {
	out := c
	if out.Backend == "" {
		out.Backend = CoordinationBackendMemory
	}
	if out.Lease.TTL <= 0 {
		out.Lease.TTL = DefaultLeaseTTL
	}
	if out.Lease.RenewInterval <= 0 {
		out.Lease.RenewInterval = out.Lease.TTL / 3
	}
	if out.Lease.RetryInterval <= 0 {
		out.Lease.RetryInterval = DefaultLeaseRetryInterval
	}
	return out
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
