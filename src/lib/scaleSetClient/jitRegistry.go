package scaleSetClient

import (
	"context"
	"sync"
)

// JitRegistry maps a tray type name to its JitConfigGenerator. It is populated
// once at startup (one entry per tray type) and read by the agent HTTP handlers
// on every replica.
//
// It decouples tray registration from leadership: generating a JIT runner
// config is a sessionless GitHub call, so any replica can serve it regardless
// of which replica holds that tray type's scale set session. Without this, the
// register handler would have to reach into the (leader-only) poller.
type JitRegistry struct {
	mu   sync.RWMutex
	gens map[string]JitConfigGenerator
}

func NewJitRegistry() *JitRegistry {
	return &JitRegistry{gens: make(map[string]JitConfigGenerator)}
}

// Register associates a JitConfigGenerator with a tray type name.
func (r *JitRegistry) Register(trayTypeName string, gen JitConfigGenerator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gens[trayTypeName] = gen
}

// Get returns the generator for a tray type, or nil if none is registered.
func (r *JitRegistry) Get(trayTypeName string) JitConfigGenerator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gens[trayTypeName]
}

// RemoveRunner deregisters a runner through its tray type's client (see
// ScaleSetClient.RemoveRunner). A tray type that is no longer configured has
// no client, so nothing can be asked about its runners and nil is returned.
func (r *JitRegistry) RemoveRunner(ctx context.Context, trayTypeName string, runnerId int64) error {
	gen := r.Get(trayTypeName)
	if gen == nil {
		return nil
	}
	return gen.RemoveRunner(ctx, runnerId)
}
