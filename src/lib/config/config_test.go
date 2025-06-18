package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig(t *testing.T) {
	// Test case 1: Valid config file
	t.Run("ValidConfigFile", func(t *testing.T) {
		// Create a temporary config file
		tempFile, err := os.CreateTemp("", "config*.yaml")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tempFile.Name())

		// Write valid config content
		validConfig := `
server:
  listenAddress: ":8080"
  advertiseUrl: "http://localhost:8080"
database:
  uri: "mongodb://localhost:27017"
  database: "cattery"
github:
  - name: "test-org"
    appId: 12345
    installationId: 67890
    webhookSecret: "secret"
    privateKeyPath: "path/to/key.pem"
providers:
  - name: "docker"
    type: "docker"
trayTypes:
  - name: "default"
    provider: "docker"
    runnerGroupId: 1
    githubOrg: "test-org"
    maxTrays: 5
`
		_, err = tempFile.Write([]byte(validConfig))
		if err != nil {
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		tempFile.Close()

		// Test loading the config
		configPath := tempFile.Name()
		config, err := LoadConfig(&configPath)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, ":8080", config.Server.ListenAddress)
		assert.Equal(t, "http://localhost:8080", config.Server.AdvertiseUrl)
		assert.Equal(t, "mongodb://localhost:27017", config.Database.Uri)
		assert.Equal(t, "cattery", config.Database.Database)
		assert.Len(t, config.Github, 1)
		assert.Equal(t, "test-org", config.Github[0].Name)
		assert.Len(t, config.Providers, 1)
		assert.Equal(t, "docker", config.Providers[0].Get("name"))
		assert.Len(t, config.TrayTypes, 1)
		assert.Equal(t, "default", config.TrayTypes[0].Name)
	})

	// Test case 2: Config file not found
	t.Run("ConfigFileNotFound", func(t *testing.T) {
		nonExistentPath := "non_existent_config.yaml"
		config, err := LoadConfig(&nonExistentPath)

		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "system cannot find the file specified")
	})

	// Test case 3: Invalid config file (validation failure)
	t.Run("InvalidConfigFile", func(t *testing.T) {
		// Create a temporary config file with invalid content
		tempFile, err := os.CreateTemp("", "invalid_config*.yaml")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tempFile.Name())

		// Write invalid config content (missing required fields)
		invalidConfig := `
server:
  listenAddress: ":8080"
  # Missing advertiseUrl
database:
  uri: "mongodb://localhost:27017"
  database: "cattery"
# Missing github section
providers:
  - name: "docker"
    type: "docker"
# Missing trayTypes section
`
		_, err = tempFile.Write([]byte(invalidConfig))
		if err != nil {
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		tempFile.Close()

		// Test loading the config
		configPath := tempFile.Name()
		config, err := LoadConfig(&configPath)

		// Assertions
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "Validation failed")
	})
}

func TestGetGitHubOrg(t *testing.T) {
	// Setup test config
	config := &CatteryConfig{
		githubMap: map[string]*GitHubOrganization{
			"test-org": {
				Name:           "test-org",
				AppId:          12345,
				InstallationId: 67890,
				WebhookSecret:  "secret",
				PrivateKeyPath: "path/to/key.pem",
			},
		},
	}

	// Test case 1: Existing organization
	t.Run("ExistingOrg", func(t *testing.T) {
		org := config.GetGitHubOrg("test-org")
		assert.NotNil(t, org)
		assert.Equal(t, "test-org", org.Name)
		assert.Equal(t, int64(12345), org.AppId)
		assert.Equal(t, int64(67890), org.InstallationId)
	})

	// Test case 2: Non-existing organization
	t.Run("NonExistingOrg", func(t *testing.T) {
		org := config.GetGitHubOrg("non-existing-org")
		assert.Nil(t, org)
	})
}

func TestGetProvider(t *testing.T) {
	// Setup test config
	config := &CatteryConfig{
		providerMap: map[string]*ProviderConfig{
			"docker": {
				"name": "docker",
				"type": "docker",
			},
		},
	}

	// Test case 1: Existing provider
	t.Run("ExistingProvider", func(t *testing.T) {
		provider := config.GetProvider("docker")
		assert.NotNil(t, provider)
		assert.Equal(t, "docker", (*provider)["name"])
		assert.Equal(t, "docker", (*provider)["type"])
	})

	// Test case 2: Non-existing provider
	t.Run("NonExistingProvider", func(t *testing.T) {
		provider := config.GetProvider("non-existing-provider")
		assert.Nil(t, provider)
	})
}

func TestGetTrayType(t *testing.T) {
	// Setup test config
	config := &CatteryConfig{
		trayTypesMap: map[string]*TrayType{
			"default": {
				Name:          "default",
				Provider:      "docker",
				RunnerGroupId: 1,
				GitHubOrg:     "test-org",
				MaxTrays:      5,
			},
		},
	}

	// Test case 1: Existing tray type
	t.Run("ExistingTrayType", func(t *testing.T) {
		trayType := config.GetTrayType("default")
		assert.NotNil(t, trayType)
		assert.Equal(t, "default", trayType.Name)
		assert.Equal(t, "docker", trayType.Provider)
		assert.Equal(t, int64(1), trayType.RunnerGroupId)
		assert.Equal(t, "test-org", trayType.GitHubOrg)
		assert.Equal(t, 5, trayType.MaxTrays)
	})

	// Test case 2: Non-existing tray type
	t.Run("NonExistingTrayType", func(t *testing.T) {
		trayType := config.GetTrayType("non-existing-tray-type")
		assert.Nil(t, trayType)
	})
}

func TestTrayConfigGet(t *testing.T) {
	// Setup test tray config
	trayConfig := TrayConfig{
		"name":     "test-tray",
		"provider": "docker",
	}

	// Test case 1: Existing key
	t.Run("ExistingKey", func(t *testing.T) {
		value := trayConfig.Get("name")
		assert.Equal(t, "test-tray", value)
	})

	// Test case 2: Existing key with different case
	t.Run("ExistingKeyDifferentCase", func(t *testing.T) {
		value := trayConfig.Get("NAME")
		assert.Equal(t, "test-tray", value)
	})

	// Test case 3: Non-existing key
	t.Run("NonExistingKey", func(t *testing.T) {
		value := trayConfig.Get("non-existing-key")
		assert.Equal(t, "", value)
	})
}

func TestProviderConfigGet(t *testing.T) {
	// Setup test provider config
	providerConfig := ProviderConfig{
		"name": "docker",
		"type": "docker",
	}

	// Test case 1: Existing key
	t.Run("ExistingKey", func(t *testing.T) {
		value := providerConfig.Get("name")
		assert.Equal(t, "docker", value)
	})

	// Test case 2: Existing key with different case
	t.Run("ExistingKeyDifferentCase", func(t *testing.T) {
		value := providerConfig.Get("NAME")
		assert.Equal(t, "docker", value)
	})

	// Test case 3: Non-existing key
	t.Run("NonExistingKey", func(t *testing.T) {
		value := providerConfig.Get("non-existing-key")
		assert.Equal(t, "", value)
	})
}
