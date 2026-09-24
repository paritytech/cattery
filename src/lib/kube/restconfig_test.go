package kube

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// writeKubeconfig writes a kubeconfig with two contexts ("alpha", the current
// one, with a namespace; "beta" without) and returns its path.
func writeKubeconfig(t *testing.T) string {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["alpha"] = &clientcmdapi.Cluster{Server: "https://alpha.example:6443"}
	cfg.Clusters["beta"] = &clientcmdapi.Cluster{Server: "https://beta.example:6443"}
	cfg.AuthInfos["user"] = &clientcmdapi.AuthInfo{Token: "token"}
	cfg.Contexts["alpha"] = &clientcmdapi.Context{Cluster: "alpha", AuthInfo: "user", Namespace: "alpha-ns"}
	cfg.Contexts["beta"] = &clientcmdapi.Context{Cluster: "beta", AuthInfo: "user"}
	cfg.CurrentContext = "alpha"

	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	return path
}

// notInCluster makes rest.InClusterConfig report ErrNotInCluster even if the
// test happens to run inside a pod.
func notInCluster(t *testing.T) {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
}

func TestNewRestConfig_ExplicitPath(t *testing.T) {
	path := writeKubeconfig(t)

	cfg, ns, err := NewRestConfig(Options{Kubeconfig: path})
	require.NoError(t, err)

	assert.Equal(t, "https://alpha.example:6443", cfg.Host)
	assert.Equal(t, "alpha-ns", ns)
	assert.Contains(t, cfg.UserAgent, "cattery/")
	assert.Equal(t, float32(defaultQPS), cfg.QPS)
	assert.Equal(t, defaultBurst, cfg.Burst)
}

func TestNewRestConfig_ContextOverride(t *testing.T) {
	path := writeKubeconfig(t)
	// A remote context without a namespace must resolve to the remote
	// cluster's "default", never to the namespace this process runs in.
	// client-go's Namespace() only substitutes the local namespace when a
	// service-account token is mounted at its fixed path, which a unit test
	// cannot arrange; the implementation reads the context directly and this
	// asserts the contract, not the regression.

	cfg, ns, err := NewRestConfig(Options{Kubeconfig: path, Context: "beta"})
	require.NoError(t, err)

	assert.Equal(t, "https://beta.example:6443", cfg.Host)
	assert.Equal(t, "default", ns, "a context without a namespace resolves to default")
}

func TestNewRestConfig_MissingExplicitPath(t *testing.T) {
	_, _, err := NewRestConfig(Options{Kubeconfig: filepath.Join(t.TempDir(), "missing")})
	assert.Error(t, err)
}

func TestNewRestConfig_UnknownContext(t *testing.T) {
	_, _, err := NewRestConfig(Options{Kubeconfig: writeKubeconfig(t), Context: "gamma"})
	assert.Error(t, err)
}

func TestNewRestConfig_FallsBackToAmbientKubeconfigWhenNotInCluster(t *testing.T) {
	notInCluster(t)
	t.Setenv("KUBECONFIG", writeKubeconfig(t))
	t.Setenv("POD_NAMESPACE", "ignored-outside-a-cluster")

	cfg, ns, err := NewRestConfig(Options{})
	require.NoError(t, err)

	assert.Equal(t, "https://alpha.example:6443", cfg.Host)
	assert.Equal(t, "alpha-ns", ns)
}

func TestNewRestConfig_ContextOnlyUsesAmbientKubeconfig(t *testing.T) {
	notInCluster(t)
	t.Setenv("KUBECONFIG", writeKubeconfig(t))

	cfg, _, err := NewRestConfig(Options{Context: "beta"})
	require.NoError(t, err)

	assert.Equal(t, "https://beta.example:6443", cfg.Host)
}

func TestNewRestConfig_Server(t *testing.T) {
	cfg, ns, err := NewRestConfig(Options{
		Server:    "https://target.example:6443",
		TokenFile: "/cattery/secrets/target/token",
		CAFile:    "/cattery/secrets/target/ca.crt",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://target.example:6443", cfg.Host)
	assert.Equal(t, "/cattery/secrets/target/token", cfg.BearerTokenFile)
	assert.Equal(t, "/cattery/secrets/target/ca.crt", cfg.TLSClientConfig.CAFile)
	assert.False(t, cfg.TLSClientConfig.Insecure)
	assert.Equal(t, "default", ns, "a remote cluster's namespace never comes from this pod")

	cfg, _, err = NewRestConfig(Options{Server: "https://target.example:6443", Token: "inline", Insecure: true})
	require.NoError(t, err)
	assert.Equal(t, "inline", cfg.BearerToken)
	assert.True(t, cfg.TLSClientConfig.Insecure)
}

func TestNewRestConfig_ServerErrors(t *testing.T) {
	path := writeKubeconfig(t)
	tests := map[string]Options{
		"server without token":        {Server: "https://t:6443"},
		"server with kubeconfig":      {Server: "https://t:6443", Token: "x", Kubeconfig: path},
		"server with context":         {Server: "https://t:6443", Token: "x", Context: "alpha"},
		"caFile with insecure":        {Server: "https://t:6443", Token: "x", CAFile: "ca.crt", Insecure: true},
		"token with kubeconfig":       {Kubeconfig: path, Token: "x"},
		"tokenFile with in-cluster":   {TokenFile: "f"},
		"insecure with in-cluster":    {Insecure: true},
		"caFile with kubeconfig only": {Kubeconfig: path, CAFile: "ca.crt"},
	}
	for name, o := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := NewRestConfig(o)
			assert.Error(t, err)
		})
	}
}

func TestPodNamespace(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")
	assert.Equal(t, "default", podNamespace())

	t.Setenv("POD_NAMESPACE", "pod-ns")
	assert.Equal(t, "pod-ns", podNamespace())
}
