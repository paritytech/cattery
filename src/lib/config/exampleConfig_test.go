package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped example config must keep loading: it doubles as documentation
// for every provider's config block, including the JSON-decoded kubernetes one.
// The file lives outside the Go module, so the test skips when only src/ is
// checked out (e.g. tests run from a container with just the module mounted).
func TestLoadConfig_ShippedExample(t *testing.T) {
	configPath := filepath.Join("..", "..", "..", "examples", "example-config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Skipf("example config not available: %v", err)
	}

	cfg, err := LoadConfig(&configPath)
	require.NoError(t, err)

	typed := cfg.GetTrayType("cattery-k8s")
	require.NotNil(t, typed)
	kc, ok := typed.Config.(KubernetesTrayConfig)
	require.True(t, ok, "got %T", typed.Config)
	assert.Equal(t, "ghcr.io/actions/actions-runner:2.333.0", kc.Image)
	assert.Equal(t, KubernetesAgentVersionServer, kc.AgentVersion)
	assert.Equal(t, "true", kc.NodeSelector["cloud.google.com/gke-spot"])
	assert.Len(t, kc.Tolerations, 1)
	assert.Len(t, kc.Env, 1)

	ref := cfg.GetTrayType("cattery-k8s-dind")
	require.NotNil(t, ref)
	kc, ok = ref.Config.(KubernetesTrayConfig)
	require.True(t, ok, "got %T", ref.Config)
	assert.True(t, kc.UsesPodTemplateRef())
	assert.Equal(t, "cattery-runner-dind", kc.PodTemplateRef)
	assert.Equal(t, "runner", kc.RunnerContainer)

	_, ok = cfg.GetTrayType("cattery-nomad").Config.(NomadTrayConfig)
	assert.True(t, ok)
	_, ok = cfg.GetTrayType("cattery-gce").Config.(GoogleTrayConfig)
	assert.True(t, ok)
}
