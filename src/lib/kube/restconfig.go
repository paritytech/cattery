// Package kube holds the Kubernetes client plumbing shared by cattery
// components that talk to a cluster (today: the kubernetes tray provider).
package kube

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"cattery/lib/version"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// Client-side rate limits. client-go's defaults (5 QPS / 10 burst) are
	// sized for a kubectl session; a scale-up of maxParallelCreation trays
	// issues a create plus a list/watch each, so give it headroom.
	defaultQPS   = 20
	defaultBurst = 50

	podNamespaceEnv = "POD_NAMESPACE"
	saNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// Options selects how to reach a cluster. One of three modes applies:
//
//   - Server set: talk to that API server with a bearer token (Token or
//     TokenFile) and CAFile (or Insecure). This is the mode for a cluster
//     other than the one cattery runs in, when a kubeconfig is impractical
//     (e.g. it would need a cloud auth plugin the cattery image lacks).
//   - Kubeconfig or Context set: load that kubeconfig (the ambient one when
//     Kubeconfig is empty) and use the given context.
//   - nothing set: in-cluster credentials; outside a cluster, the ambient
//     kubeconfig (KUBECONFIG / ~/.kube/config) so a dev box works.
type Options struct {
	Kubeconfig string
	Context    string

	Server    string
	Token     string
	TokenFile string
	CAFile    string
	Insecure  bool
}

// NewRestConfig builds the REST config for o and returns the namespace that
// goes with it: the pod's own namespace in-cluster, the context's namespace
// for a kubeconfig, "default" for an explicit server.
func NewRestConfig(o Options) (*rest.Config, string, error) {
	var (
		cfg *rest.Config
		ns  string
		err error
	)
	switch {
	case o.Server != "":
		cfg, ns, err = fromServer(o)
	case o.Kubeconfig != "" || o.Context != "":
		if err := o.rejectServerFields("kubeconfig/context"); err != nil {
			return nil, "", err
		}
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		if o.Kubeconfig != "" {
			rules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: o.Kubeconfig}
		}
		cfg, ns, err = fromKubeconfig(rules, o.Context)
	default:
		if err := o.rejectServerFields("in-cluster access"); err != nil {
			return nil, "", err
		}
		cfg, err = rest.InClusterConfig()
		switch {
		case err == nil:
			ns = podNamespace()
		case errors.Is(err, rest.ErrNotInCluster):
			cfg, ns, err = fromKubeconfig(clientcmd.NewDefaultClientConfigLoadingRules(), "")
		}
	}
	if err != nil {
		return nil, "", err
	}

	cfg.UserAgent = "cattery/" + version.Version
	cfg.QPS = defaultQPS
	cfg.Burst = defaultBurst
	return cfg, ns, nil
}

// rejectServerFields refuses token/CA settings outside server mode, where
// they would be silently ignored.
func (o Options) rejectServerFields(mode string) error {
	if o.Token != "" || o.TokenFile != "" || o.CAFile != "" || o.Insecure {
		return fmt.Errorf("token, tokenFile, caFile and insecure only apply together with server (not with %s)", mode)
	}
	return nil
}

func fromServer(o Options) (*rest.Config, string, error) {
	if o.Kubeconfig != "" || o.Context != "" {
		return nil, "", errors.New("server and kubeconfig/context are mutually exclusive")
	}
	if o.Token == "" && o.TokenFile == "" {
		return nil, "", errors.New("server requires token or tokenFile")
	}
	if o.Insecure && o.CAFile != "" {
		return nil, "", errors.New("caFile and insecure are mutually exclusive")
	}
	cfg := &rest.Config{
		Host:            o.Server,
		BearerToken:     o.Token,
		BearerTokenFile: o.TokenFile, // re-read by client-go, so rotated tokens keep working
		TLSClientConfig: rest.TLSClientConfig{CAFile: o.CAFile, Insecure: o.Insecure},
	}
	return cfg, "default", nil
}

func fromKubeconfig(rules *clientcmd.ClientConfigLoadingRules, contextName string) (*rest.Config, string, error) {
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: contextName})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}

	// The namespace is read off the selected context directly. client-go's
	// Namespace() would, for a context without one, fall back to the
	// namespace of the service-account mount whenever the process runs in a
	// cluster, which would point a remote-cluster provider at this pod's
	// namespace instead of the target cluster's "default".
	raw, err := cc.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("read kubeconfig: %w", err)
	}
	name := contextName
	if name == "" {
		name = raw.CurrentContext
	}
	ns := "default"
	if c := raw.Contexts[name]; c != nil && c.Namespace != "" {
		ns = c.Namespace
	}
	return cfg, ns, nil
}

// podNamespace is the namespace this process runs in: POD_NAMESPACE (set by
// the chart), then the service-account namespace file, then "default".
func podNamespace() string {
	if v := os.Getenv(podNamespaceEnv); v != "" {
		return v
	}
	if b, err := os.ReadFile(saNamespaceFile); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return "default"
}
