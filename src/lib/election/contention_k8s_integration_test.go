//go:build integration_k8s

package election

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestK8sElector_SingleLeaderAndFailover(t *testing.T) {
	client := k8sCoordinationClient(t)

	const ns = "default"
	const prefix = "cattery-test-"
	const key = "contention"
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

	var instances []electorInstance
	for i := 0; i < 3; i++ {
		id := HolderID()
		instances = append(instances, electorInstance{id, newK8sElector(client, ns, prefix, id, cfg)})
	}

	assertSingleLeaderAndFailover(t, instances, key)
}
