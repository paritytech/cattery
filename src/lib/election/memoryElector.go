package election

import "context"

// MemoryElector grants leadership immediately and holds it for the lifetime of
// ctx. It performs no coordination, so it is correct only for SINGLE-replica
// deployments (the "memory" backend, also used by sqlite): there is no shared
// state, so every process believes it leads. Running more than one replica on
// this elector means each replica tries to hold every tray type's GitHub
// session, which conflicts — use a shared backend (mongo, k8s) for HA.
type MemoryElector struct{}

// NewMemoryElector returns an Elector that always leads. Single-replica only.
func NewMemoryElector() Elector { return MemoryElector{} }

func (MemoryElector) Run(ctx context.Context, _ string, onElected OnElected) error {
	onElected(ctx) // leaderCtx == ctx: leader until shutdown
	return ctx.Err()
}
