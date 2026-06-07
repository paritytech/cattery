package election

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// K8sElector implements per-key leader election on native coordination.k8s.io
// Lease objects via client-go. One Lease per key (tray type). It must run
// in-cluster (it uses the pod's service account).
type K8sElector struct {
	// Only the coordination/v1 client is needed (not the full Clientset) since
	// we only touch Leases. Note this trims little binary size on its own — the
	// apimachinery serialization machinery that leaderelection pulls in
	// dominates — but it keeps the dependency surface honest.
	client    coordinationv1.CoordinationV1Interface
	namespace string
	prefix    string
	identity  string

	// client-go's three windows, derived from LeaseConfig:
	// leaseDuration > renewDeadline > retryPeriod.
	leaseDuration time.Duration
	renewDeadline time.Duration
	retryPeriod   time.Duration

	logger *log.Entry
}

// NewK8sElector builds a k8s-backed Elector using in-cluster credentials.
// namespace/prefix may be empty (resolved to sensible defaults). identity must
// be unique per replica (see HolderID).
func NewK8sElector(namespace, prefix, identity string, cfg LeaseConfig) (Elector, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s election requires running in-cluster: %w", err)
	}
	client, err := coordinationv1.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build k8s coordination client: %w", err)
	}
	return newK8sElector(client, namespace, prefix, identity, cfg), nil
}

// newK8sElector builds the elector from an already-constructed coordination
// client. Split from NewK8sElector so tests can inject an out-of-cluster client
// (built from a kubeconfig) instead of requiring in-cluster credentials.
func newK8sElector(client coordinationv1.CoordinationV1Interface, namespace, prefix, identity string, cfg LeaseConfig) *K8sElector {
	cfg = cfg.withDefaults()

	if prefix == "" {
		prefix = "cattery-"
	}

	// Map our lease model onto client-go's. client-go requires
	// leaseDuration > renewDeadline > retryPeriod; clamp defensively.
	leaseDuration := cfg.TTL
	retryPeriod := cfg.RenewInterval
	renewDeadline := cfg.TTL * 2 / 3
	if renewDeadline <= retryPeriod {
		renewDeadline = retryPeriod + retryPeriod/2
	}
	if leaseDuration <= renewDeadline {
		leaseDuration = renewDeadline + renewDeadline/2
	}

	return &K8sElector{
		client:        client,
		namespace:     resolveNamespace(namespace),
		prefix:        prefix,
		identity:      identity,
		leaseDuration: leaseDuration,
		renewDeadline: renewDeadline,
		retryPeriod:   retryPeriod,
		logger:        log.WithField("component", "election"),
	}
}

func (e *K8sElector) Run(ctx context.Context, key string, onElected OnElected) error {
	logger := e.logger.WithField("key", key)
	for ctx.Err() == nil {
		if err := e.runOnce(ctx, key, logger, onElected); err != nil {
			return err // config error — deterministic, no point retrying
		}
		// le.Run blocks on its own acquire loop, so this only spins after a
		// leadership loss; a small jittered pause avoids a hot loop in edge cases.
		if !sleep(ctx, jitter(e.retryPeriod)) {
			break
		}
	}
	return ctx.Err()
}

// runOnce contends for the Lease once: it blocks in le.Run until leadership is
// lost or ctx ends, then — if leadership was actually held — waits for
// onElected to fully return before allowing Run to re-contend, so the same
// replica never runs two leadership terms for a key concurrently.
func (e *K8sElector) runOnce(ctx context.Context, key string, logger *log.Entry, onElected OnElected) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      e.leaseName(key),
			Namespace: e.namespace,
		},
		Client:     e.client,
		LockConfig: resourcelock.ResourceLockConfig{Identity: e.identity},
	}

	started := make(chan struct{})
	finished := make(chan struct{})

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.leaseDuration,
		RenewDeadline:   e.renewDeadline,
		RetryPeriod:     e.retryPeriod,
		ReleaseOnCancel: true, // hand the Lease back on shutdown → instant failover
		Name:            key,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				logger.Info("Acquired leadership")
				close(started)
				onElected(leaderCtx)
				close(finished)
			},
			OnStoppedLeading: func() {
				logger.Info("Lost leadership")
			},
		},
	})
	if err != nil {
		return fmt.Errorf("invalid leader election config: %w", err)
	}

	le.Run(ctx)

	// If we ever started leading, OnStartedLeading is running onElected in a
	// goroutine; wait for it to unwind before re-contending.
	select {
	case <-started:
		<-finished
	default:
	}
	return nil
}

// leaseName builds a DNS-subdomain-safe Lease object name from the key.
func (e *K8sElector) leaseName(key string) string {
	return e.prefix + sanitizeName(key)
}

// sanitizeName lowercases key and replaces any char that is not [a-z0-9-] with
// '-', so an arbitrary tray type name yields a valid Lease object name.
func sanitizeName(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// resolveNamespace returns ns, or discovers the pod's namespace from the
// POD_NAMESPACE env var, then the service-account namespace file, then "default".
func resolveNamespace(ns string) string {
	if ns != "" {
		return ns
	}
	if v := os.Getenv("POD_NAMESPACE"); v != "" {
		return v
	}
	const saNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if b, err := os.ReadFile(saNamespaceFile); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return "default"
}
