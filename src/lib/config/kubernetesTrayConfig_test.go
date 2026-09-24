package config

import (
	"os"
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeK8s mirrors the config loader: mapstructure over the lowercased map
// viper produces.
func decodeK8s(t *testing.T, raw map[string]any) KubernetesTrayConfig {
	t.Helper()
	var kc KubernetesTrayConfig
	require.NoError(t, mapstructure.Decode(raw, &kc))
	return kc
}

func TestKubernetesTrayConfig_DecodesWithMapstructure(t *testing.T) {
	kc := decodeK8s(t, map[string]any{
		"image":                        "ghcr.io/actions/actions-runner:2.333.0",
		"imagepullpolicy":              "IfNotPresent",
		"imagepullsecrets":             []any{"regcred"},
		"serviceaccountname":           "runner",
		"automountserviceaccounttoken": true,
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "2", "memory": "4Gi"},
			"limits":   map[string]any{"memory": "4Gi"},
		},
		"nodeselector":                  map[string]any{"kubernetes.io/os": "linux"},
		"tolerations":                   []any{map[string]any{"key": "ci", "operator": "Equal", "value": "true", "effect": "NoSchedule", "tolerationseconds": 30}},
		"labels":                        map[string]any{"team": "ci"},
		"annotations":                   map[string]any{"cluster-autoscaler.kubernetes.io/safe-to-evict": "false"},
		"env":                           []any{map[string]any{"name": "FOO", "value": "bar"}},
		"runtimeclassname":              "gvisor",
		"priorityclassname":             "ci",
		"terminationgraceperiodseconds": 45,
		"ttlsecondsafterfinished":       600,
		"activedeadlineseconds":         7200,
		"agentversion":                  "0.2.0",
		"agentimage":                    "registry.internal/cattery:0.2.0",
		"runnerfolder":                  "/runner",
	})

	assert.Equal(t, "ghcr.io/actions/actions-runner:2.333.0", kc.Image)
	assert.Equal(t, "IfNotPresent", kc.ImagePullPolicy)
	assert.Equal(t, []string{"regcred"}, kc.ImagePullSecrets)
	assert.Equal(t, "runner", kc.ServiceAccountName)
	require.NotNil(t, kc.AutomountServiceAccountToken)
	assert.True(t, *kc.AutomountServiceAccountToken)
	assert.Equal(t, map[string]string{"cpu": "2", "memory": "4Gi"}, kc.Resources.Requests)
	assert.Equal(t, map[string]string{"memory": "4Gi"}, kc.Resources.Limits)
	assert.Equal(t, map[string]string{"kubernetes.io/os": "linux"}, kc.NodeSelector)
	require.Len(t, kc.Tolerations, 1)
	assert.Equal(t, "Equal", kc.Tolerations[0].Operator)
	assert.Equal(t, "NoSchedule", kc.Tolerations[0].Effect)
	require.NotNil(t, kc.Tolerations[0].TolerationSeconds)
	assert.Equal(t, int64(30), *kc.Tolerations[0].TolerationSeconds)
	assert.Equal(t, map[string]string{"team": "ci"}, kc.Labels)
	assert.Equal(t, "false", kc.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"])
	assert.Equal(t, []KubernetesEnvVar{{Name: "FOO", Value: "bar"}}, kc.Env)
	assert.Equal(t, "gvisor", kc.RuntimeClassName)
	assert.Equal(t, "ci", kc.PriorityClassName)
	require.NotNil(t, kc.TerminationGracePeriodSeconds)
	assert.Equal(t, int64(45), *kc.TerminationGracePeriodSeconds)
	assert.Equal(t, int32(600), kc.TTLSecondsAfterFinishedOrDefault())
	require.NotNil(t, kc.ActiveDeadlineSeconds)
	assert.Equal(t, int64(7200), *kc.ActiveDeadlineSeconds)
	assert.Equal(t, "0.2.0", kc.AgentVersion)
	assert.Equal(t, "registry.internal/cattery:0.2.0", kc.AgentImage)
	assert.Equal(t, "/runner", kc.RunnerFolder)
	assert.False(t, kc.UsesPodTemplateRef())
}

func TestKubernetesTrayConfig_UnsetFields(t *testing.T) {
	kc := decodeK8s(t, map[string]any{"image": "img"})

	assert.Empty(t, kc.AgentVersion, "defaults are applied by the provider, not the decoder")
	assert.Empty(t, kc.RunnerFolder)
	assert.Empty(t, kc.RunnerContainer)
	assert.Nil(t, kc.TTLSecondsAfterFinished)
	assert.Equal(t, DefaultKubernetesTTLSecondsAfterFinished, kc.TTLSecondsAfterFinishedOrDefault())
	assert.Nil(t, kc.ActiveDeadlineSeconds)
	assert.Nil(t, kc.AutomountServiceAccountToken)
}

func TestKubernetesTrayConfig_QuantitiesAndEnvValuesMustBeStrings(t *testing.T) {
	// YAML `cpu: 2` arrives as an int; the decoder is not weakly typed, so
	// the operator has to quote it. Make sure that fails loudly.
	var kc KubernetesTrayConfig
	err := mapstructure.Decode(map[string]any{
		"image":     "img",
		"resources": map[string]any{"requests": map[string]any{"cpu": 2}},
	}, &kc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cpu")

	err = mapstructure.Decode(map[string]any{
		"image": "img",
		"env":   []any{map[string]any{"name": "DEBUG", "value": true}},
	}, &kc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Env[0].Value")
}

func TestKubernetesTrayConfig_Validate(t *testing.T) {
	negative := int32(-1)
	tests := []struct {
		name         string
		trayTypeName string
		kc           KubernetesTrayConfig
		wantErr      string
	}{
		{name: "typed ok", trayTypeName: "k8s-small", kc: KubernetesTrayConfig{Image: "img"}},
		{name: "ref ok", trayTypeName: "k8s-large", kc: KubernetesTrayConfig{PodTemplateRef: "tpl"}},
		{name: "name at limit", trayTypeName: strings.Repeat("a", MaxKubernetesTrayTypeNameLength), kc: KubernetesTrayConfig{Image: "img"}},
		{name: "full typed config", trayTypeName: "k8s", kc: KubernetesTrayConfig{
			Image:           "img",
			ImagePullPolicy: "Always",
			Resources:       KubernetesResources{Requests: map[string]string{"cpu": "500m", "memory": "1Gi"}, Limits: map[string]string{"nvidia.com/gpu": "1"}},
			Tolerations:     []KubernetesToleration{{Key: "ci", Operator: "Exists", Effect: "NoSchedule"}},
			Env:             []KubernetesEnvVar{{Name: "A", Value: "1"}},
		}},
		{name: "both image and ref", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", PodTemplateRef: "tpl"}, wantErr: "mutually exclusive"},
		{name: "neither image nor ref", trayTypeName: "k8s", kc: KubernetesTrayConfig{}, wantErr: "one of image or podTemplateRef"},
		{name: "uppercase name", trayTypeName: "My_Runner", kc: KubernetesTrayConfig{Image: "img"}, wantErr: "DNS-1123"},
		{name: "name too long", trayTypeName: strings.Repeat("a", MaxKubernetesTrayTypeNameLength+1), kc: KubernetesTrayConfig{Image: "img"}, wantErr: "longer than"},
		{name: "bad pull policy", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", ImagePullPolicy: "Sometimes"}, wantErr: "imagePullPolicy"},
		{name: "bad quantity", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", Resources: KubernetesResources{Limits: map[string]string{"memory": "4 gigs"}}}, wantErr: "resources.limits.memory"},
		{name: "bad agent quantity", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", AgentResources: KubernetesResources{Requests: map[string]string{"cpu": "fast"}}}, wantErr: "agentResources.requests.cpu"},
		{name: "bad toleration operator", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", Tolerations: []KubernetesToleration{{Key: "k", Operator: "Like"}}}, wantErr: "tolerations[0].operator"},
		{name: "bad toleration effect", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", Tolerations: []KubernetesToleration{{Key: "k", Effect: "Never"}}}, wantErr: "tolerations[0].effect"},
		{name: "env without name", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", Env: []KubernetesEnvVar{{Value: "x"}}}, wantErr: "env[0].name"},
		{name: "negative ttl", trayTypeName: "k8s", kc: KubernetesTrayConfig{Image: "img", TTLSecondsAfterFinished: &negative}, wantErr: "ttlSecondsAfterFinished"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.kc.Validate(tc.trayTypeName)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestKubernetesTrayConfig_ValidateWarnsAboutIgnoredFields(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()

	kc := KubernetesTrayConfig{
		PodTemplateRef: "tpl",
		NodeSelector:   map[string]string{"a": "b"},
		Env:            []KubernetesEnvVar{{Name: "X", Value: "1"}},
	}
	require.NoError(t, kc.Validate("k8s"))

	var messages []string
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			messages = append(messages, e.Message)
		}
	}
	assert.Equal(t, []string{
		"trayType k8s: config.nodeSelector is ignored because podTemplateRef is set",
		"trayType k8s: config.env is ignored because podTemplateRef is set",
	}, messages)

	hook.Reset()
	require.NoError(t, KubernetesTrayConfig{Image: "img", RunnerContainer: "worker"}.Validate("k8s"))
	require.Len(t, hook.AllEntries(), 1)
	assert.Contains(t, hook.LastEntry().Message, "runnerContainer only applies with podTemplateRef")
}

const kubernetesConfigPreamble = `
server:
  listenAddress: ":8080"
  advertiseUrl: "http://localhost:8080"
database:
  uri: "mongodb://localhost:27017"
  database: "cattery"
github:
  - name: "test-org"
    appId: 12345
    appClientId: "Iv1.test123"
    installationId: 67890
    privateKeyPath: "path/to/key.pem"
providers:
  - name: "k8s-local"
    type: "kubernetes"
    deployTimeout: 3m
`

func loadConfigFromString(t *testing.T, content string) (*CatteryConfig, error) {
	t.Helper()
	tempFile, err := os.CreateTemp("", "config_k8s*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(tempFile.Name()) })
	_, err = tempFile.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tempFile.Close())

	configPath := tempFile.Name()
	return LoadConfig(&configPath)
}

// The YAML deliberately uses mixed-case keys: viper lowercases them and
// mapstructure's case-insensitive matching must still fill the right fields.
func TestLoadConfig_KubernetesTrayType(t *testing.T) {
	cfg, err := loadConfigFromString(t, kubernetesConfigPreamble+`
trayTypes:
  - name: "k8s-runner"
    provider: "k8s-local"
    runnerGroupId: 1
    githubOrg: "test-org"
    maxTrays: 2
    config:
      image: ghcr.io/actions/actions-runner:2.333.0
      imagePullPolicy: Always
      resources:
        requests:
          cpu: "2"
          memory: 4Gi
      nodeSelector:
        kubernetes.io/os: linux
      labels:
        team: ci
      env:
        - name: FOO
          value: "bar"
      tolerations:
        - key: ci
          operator: Exists
          effect: NoSchedule
      ttlSecondsAfterFinished: 120
`)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	tt := cfg.GetTrayType("k8s-runner")
	require.NotNil(t, tt)
	kc, ok := tt.Config.(KubernetesTrayConfig)
	require.True(t, ok, "config must be decoded into KubernetesTrayConfig, got %T", tt.Config)

	assert.Equal(t, "ghcr.io/actions/actions-runner:2.333.0", kc.Image)
	assert.Equal(t, "Always", kc.ImagePullPolicy)
	assert.Equal(t, map[string]string{"cpu": "2", "memory": "4Gi"}, kc.Resources.Requests)
	assert.Equal(t, "linux", kc.NodeSelector["kubernetes.io/os"])
	assert.Equal(t, "ci", kc.Labels["team"])
	assert.Equal(t, []KubernetesEnvVar{{Name: "FOO", Value: "bar"}}, kc.Env)
	require.Len(t, kc.Tolerations, 1)
	assert.Equal(t, "Exists", kc.Tolerations[0].Operator)
	assert.Equal(t, int32(120), kc.TTLSecondsAfterFinishedOrDefault())
}

func TestLoadConfig_KubernetesTrayType_InvalidConfigFailsLoad(t *testing.T) {
	_, err := loadConfigFromString(t, kubernetesConfigPreamble+`
trayTypes:
  - name: "k8s-runner"
    provider: "k8s-local"
    runnerGroupId: 1
    githubOrg: "test-org"
    maxTrays: 2
    config:
      image: ghcr.io/actions/actions-runner:2.333.0
      podTemplateRef: runner-large
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestLoadConfig_KubernetesTrayType_BadQuantityFailsLoad(t *testing.T) {
	_, err := loadConfigFromString(t, kubernetesConfigPreamble+`
trayTypes:
  - name: "k8s-runner"
    provider: "k8s-local"
    runnerGroupId: 1
    githubOrg: "test-org"
    maxTrays: 2
    config:
      image: ghcr.io/actions/actions-runner:2.333.0
      resources:
        requests:
          memory: "four gigs"
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resources.requests.memory")
}
