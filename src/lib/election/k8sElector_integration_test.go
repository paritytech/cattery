//go:build integration_k8s

package election

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// k8sCoordinationClient builds a coordination/v1 client from the ambient
// kubeconfig (KUBECONFIG / ~/.kube/config), i.e. out-of-cluster — the way a
// test on a dev box talks to the local cluster.
func k8sCoordinationClient(t *testing.T) coordinationv1.CoordinationV1Interface {
	t.Helper()
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	require.NoError(t, err, "load kubeconfig")

	client, err := coordinationv1.NewForConfig(restCfg)
	require.NoError(t, err)
	return client
}

func waitClosedT(t *testing.T, ch <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// runLeader starts an elector for key and returns once it has acquired
// leadership, along with a cancel func and a channel closed when Run returns.
func runLeader(t *testing.T, client coordinationv1.CoordinationV1Interface, ns, prefix, key, identity string, cfg LeaseConfig) (cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	elector := newK8sElector(client, ns, prefix, identity, cfg)

	ctx, cancelFn := context.WithCancel(context.Background())
	started := make(chan struct{})
	d := make(chan struct{})
	go func() {
		_ = elector.Run(ctx, key, func(lctx context.Context) {
			close(started)
			<-lctx.Done()
		})
		close(d)
	}()
	waitClosedT(t, started, 15*time.Second, "leadership acquisition for "+identity)
	return cancelFn, d
}

// Drives leadership against a real cluster end to end: leader 1 acquires the
// Lease (verified on the Lease object), steps down, then leader 2 takes over —
// whether the lease was released cleanly or reclaimed after expiry.
func TestK8sElector_LeadershipAndFailover(t *testing.T) {
	client := k8sCoordinationClient(t)

	const ns = "default"
	const prefix = "cattery-test-"
	const key = "ittest"
	leaseName := prefix + sanitizeName(key)
	leases := client.Leases(ns)

	_ = leases.Delete(context.Background(), leaseName, metav1.DeleteOptions{})
	t.Cleanup(func() {
		_ = leases.Delete(context.Background(), leaseName, metav1.DeleteOptions{})
	})

	cfg := LeaseConfig{
		TTL:           4 * time.Second,
		RenewInterval: 1 * time.Second,
		RetryInterval: 500 * time.Millisecond,
	}

	// --- leader 1 acquires ---
	id1 := HolderID()
	cancel1, done1 := runLeader(t, client, ns, prefix, key, id1, cfg)

	lease, err := leases.Get(context.Background(), leaseName, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, lease.Spec.HolderIdentity)
	assert.Equal(t, id1, *lease.Spec.HolderIdentity, "leader 1 holds the lease")

	// --- leader 1 steps down ---
	cancel1()
	waitClosedT(t, done1, 15*time.Second, "leader 1 to stop")

	// --- leader 2 takes over (clean release or expiry) ---
	id2 := HolderID()
	cancel2, done2 := runLeader(t, client, ns, prefix, key, id2, cfg)
	defer func() {
		cancel2()
		waitClosedT(t, done2, 15*time.Second, "leader 2 to stop")
	}()

	lease, err = leases.Get(context.Background(), leaseName, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, lease.Spec.HolderIdentity)
	assert.Equal(t, id2, *lease.Spec.HolderIdentity, "leader 2 took over the lease")
}
