package election

import (
	"cattery/lib/config"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// NewFromConfig builds the Elector selected by cfg. A single Elector instance
// is meant to be shared across all keys (tray types): its Run method is safe to
// call concurrently with distinct keys, and the holder identity is fixed once
// here so every key is leased under the same replica id.
//
// For the "mongo" backend leaseCollection must be non-nil. cfg is expected to
// have defaults already applied via config.CoordinationConfig.WithDefaults.
func NewFromConfig(cfg config.CoordinationConfig, leaseCollection *mongo.Collection) (Elector, error) {
	lease := LeaseConfig{
		TTL:           cfg.Lease.TTL,
		RenewInterval: cfg.Lease.RenewInterval,
		RetryInterval: cfg.Lease.RetryInterval,
	}

	switch cfg.Backend {
	case config.CoordinationBackendMemory:
		return NewMemoryElector(), nil

	case config.CoordinationBackendMongo:
		if leaseCollection == nil {
			return nil, fmt.Errorf("mongo coordination backend requires a lease collection")
		}
		store := NewMongoLeaseStore()
		store.Connect(leaseCollection)
		return NewLeaseElector(store, HolderID(), lease), nil

	case config.CoordinationBackendK8s:
		return NewK8sElector(cfg.Kubernetes.Namespace, cfg.Kubernetes.LeaseNamePrefix, HolderID(), lease)

	default:
		return nil, fmt.Errorf("unknown coordination backend %q", cfg.Backend)
	}
}
