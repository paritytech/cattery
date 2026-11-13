package scaleSetClient

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrNoClient is returned by JitRegistry.RemoveRunner when the tray type has
// no client that can deregister runners.
var ErrNoClient = errors.New("no scale set client for tray type")

// JitRegistry maps a tray type name to its JitConfigGenerator. It is populated
// once at startup (one entry per tray type) and read by the agent HTTP handlers
// on every replica.
//
// It decouples tray registration from leadership: generating a JIT runner
// config is a sessionless GitHub call, so any replica can serve it regardless
// of which replica holds that tray type's scale set session. Without this, the
// register handler would have to reach into the (leader-only) poller.
//
// Deregistering a runner is sessionless too, so RemoveRunner is routed through
// the same entries.
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

// RemoveRunner deregisters a runner through the tray type's client. It returns
// ErrNoClient when there is none (for example the tray type was removed from
// the config), so callers can tell "cannot ask GitHub" from "GitHub refused".
func (r *JitRegistry) RemoveRunner(ctx context.Context, trayTypeName string, runnerId int64) error {
	remover, ok := r.Get(trayTypeName).(RunnerRemover)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoClient, trayTypeName)
	}
	return remover.RemoveRunner(ctx, runnerId)
}
