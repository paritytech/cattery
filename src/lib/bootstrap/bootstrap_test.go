package bootstrap

import (
	"cattery/lib/config"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate_DefaultsApplied(t *testing.T) {
	script, err := Generate(config.BootstrapConfig{Enabled: true}, Params{
		ServerURL: "https://cattery.example.com",
		AgentID:   "tray-abc",
	})
	require.NoError(t, err)

	assert.Contains(t, script, "https://cattery.example.com")
	assert.Contains(t, script, "tray-abc")
	assert.Contains(t, script, DefaultAgentFolder)
	assert.Contains(t, script, DefaultRunnerFolder)
	// No User -> no sudo branch.
	assert.NotContains(t, script, "sudo -E -u")
}

func TestGenerate_CustomFolders(t *testing.T) {
	script, err := Generate(config.BootstrapConfig{
		Enabled:      true,
		AgentFolder:  "/var/cattery",
		RunnerFolder: "/var/runner",
	}, Params{
		ServerURL: "https://srv",
		AgentID:   "id1",
	})
	require.NoError(t, err)

	assert.Contains(t, script, `AGENT_FOLDER="/var/cattery"`)
	assert.Contains(t, script, `RUNNER_FOLDER="/var/runner"`)
	assert.NotContains(t, script, DefaultAgentFolder)
}

func TestGenerate_UserAddsSudo(t *testing.T) {
	script, err := Generate(config.BootstrapConfig{
		Enabled: true,
		User:    "cattery",
	}, Params{
		ServerURL: "https://srv",
		AgentID:   "id1",
	})
	require.NoError(t, err)

	assert.Contains(t, script, `sudo -E -u "cattery"`)
	assert.Contains(t, script, `chown -R "cattery":"cattery"`)
}

func TestGenerate_ScriptOverride(t *testing.T) {
	tmpl := "echo {{.ServerURL}} {{.AgentID}} {{.AgentFolder}}"
	script, err := Generate(config.BootstrapConfig{
		Enabled: true,
		Script:  tmpl,
	}, Params{
		ServerURL: "https://srv",
		AgentID:   "id1",
	})
	require.NoError(t, err)
	// Script override bypasses the built-in template entirely.
	assert.Equal(t, "echo https://srv id1 "+DefaultAgentFolder, script)
}

func TestGenerate_UnknownOSReturnsError(t *testing.T) {
	_, err := Generate(config.BootstrapConfig{
		Enabled: true,
		OS:      "haiku",
	}, Params{ServerURL: "x", AgentID: "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "haiku")
}

func TestGenerate_LinuxTemplateSyntax(t *testing.T) {
	// Sanity check: the rendered linux script begins with a shebang and is
	// not a stray template fragment.
	script, err := Generate(config.BootstrapConfig{Enabled: true}, Params{
		ServerURL: "https://srv",
		AgentID:   "id1",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(script, "#!/bin/bash"))
	assert.Contains(t, script, "set -euo pipefail")
}
