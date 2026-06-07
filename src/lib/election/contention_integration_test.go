//go:build integration_mongo || integration_k8s

package election

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Shared contention harness used by both backend-specific failover tests
// (TestMongoElector_SingleLeaderAndFailover, TestK8sElector_SingleLeaderAndFailover).
// It is backend-agnostic — it only drives the Elector interface — so it compiles
// under either integration tag.

type electorInstance struct {
	identity string
	elector  Elector
}

func waitLeader(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(timeout):
		t.Fatal("no leader was elected")
		return ""
	}
}

func waitDifferentLeader(t *testing.T, ch <-chan string, not string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case id := <-ch:
			if id != not {
				return id
			}
		case <-deadline:
			t.Fatalf("no failover leader (still %q)", not)
			return ""
		}
	}
}

// assertSingleLeaderAndFailover runs all instances against the same key and
// verifies the core Elector contract on a real backend: at most one leader at a
// time, and when the current leader stops, a different instance takes over.
func assertSingleLeaderAndFailover(t *testing.T, instances []electorInstance, key string) {
	t.Helper()

	var leading, maxLeading int32
	leaderCh := make(chan string, 16)
	cancels := make(map[string]context.CancelFunc, len(instances))
	var wg sync.WaitGroup

	for _, inst := range instances {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[inst.identity] = cancel

		id, e := inst.identity, inst.elector
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.Run(ctx, key, func(leaderCtx context.Context) {
				n := atomic.AddInt32(&leading, 1)
				for {
					m := atomic.LoadInt32(&maxLeading)
					if n <= m || atomic.CompareAndSwapInt32(&maxLeading, m, n) {
						break
					}
				}
				leaderCh <- id
				<-leaderCtx.Done()
				atomic.AddInt32(&leading, -1)
			})
		}()
	}
	t.Cleanup(func() {
		for _, c := range cancels {
			c()
		}
		wg.Wait()
	})

	// one instance becomes leader
	leader1 := waitLeader(t, leaderCh, 20*time.Second)

	// the leader steps down; a *different* instance must take over
	cancels[leader1]()
	leader2 := waitDifferentLeader(t, leaderCh, leader1, 20*time.Second)
	assert.NotEqual(t, leader1, leader2, "a different instance took over")

	assert.Equal(t, int32(1), atomic.LoadInt32(&maxLeading), "never more than one leader at a time")
}
