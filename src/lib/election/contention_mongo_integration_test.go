//go:build integration_mongo

package election

import (
	"testing"
	"time"
)

func TestMongoElector_SingleLeaderAndFailover(t *testing.T) {
	coll := setupLeaseCollection(t)
	cfg := LeaseConfig{
		TTL:           1 * time.Second,
		RenewInterval: 300 * time.Millisecond,
		RetryInterval: 200 * time.Millisecond,
	}

	var instances []electorInstance
	for i := 0; i < 3; i++ {
		store := NewMongoLeaseStore()
		store.Connect(coll) // all share one collection → real contention
		id := HolderID()
		instances = append(instances, electorInstance{id, NewLeaseElector(store, id, cfg)})
	}

	assertSingleLeaderAndFailover(t, instances, "contention")
}
