// Package election provides per-key leader election for cattery.
//
// Each tray type runs an independent election (the key is the tray type name),
// so leadership can be distributed across replicas. Only the leader for a key
// runs that tray type's scale set poller; every replica keeps serving the
// (sessionless) tray HTTP plane regardless of which leases it holds.
//
// The Elector interface is lifecycle-shaped on purpose: leadership is expressed
// as a context that is cancelled the instant leadership is lost. That shape
// fits both renew-loop backends (Mongo, SQL lease tables) and callback/native
// backends (k8s client-go Lease, Postgres advisory locks) without the poller
// ever needing to know which is in use.
package election

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
)

// OnElected is invoked when leadership for a key is acquired. The provided
// leaderCtx is cancelled the moment leadership is lost — lease expiry, renew
// failure, graceful step-down, or the parent ctx being cancelled. The callback
// owns the leader-only work (the scale set poller) and must return promptly
// once leaderCtx is done.
type OnElected func(leaderCtx context.Context)

// Elector runs leader election for a single key.
type Elector interface {
	// Run blocks until ctx is done. Each time leadership for key is acquired it
	// invokes onElected with a leaderCtx scoped to that leadership term, and
	// waits for onElected to return before attempting to (re)acquire. Run keeps
	// retrying for the lifetime of ctx, so a replica that loses or never wins
	// leadership will take over whenever the key next becomes free.
	Run(ctx context.Context, key string, onElected OnElected) error
}

// HolderID returns a stable-per-process, unique-across-replicas identity used
// as the lease holder. Hostname makes it greppable in the DB; the random
// suffix disambiguates two processes on the same host and survives restarts
// (a restarted replica must not be mistaken for its previous incarnation).
func HolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%s", host, uuid.NewString())
}
