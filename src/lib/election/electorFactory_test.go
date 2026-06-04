package election

import (
	"cattery/lib/config"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestNewFromConfig_Memory(t *testing.T) {
	e, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendMemory}, nil)
	require.NoError(t, err)
	assert.IsType(t, MemoryElector{}, e)
}

func TestNewFromConfig_MongoRequiresCollection(t *testing.T) {
	_, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendMongo}, nil)
	assert.Error(t, err, "mongo backend without a collection must error")
}

// With a collection the mongo backend yields a lease-loop elector. mongo.Connect
// is non-blocking in the v2 driver, so no live server is needed to build the
// collection handle and assert the wiring.
func TestNewFromConfig_MongoReturnsLeaseElector(t *testing.T) {
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://localhost"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	coll := client.Database("test").Collection("leases")
	e, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendMongo}, coll)
	require.NoError(t, err)
	assert.IsType(t, &LeaseElector{}, e)
}

// The k8s backend builds its client from in-cluster credentials, so constructing
// it out-of-cluster (the test environment) must fail deterministically rather
// than silently fall back. Skipped if the suite itself runs inside a pod.
func TestNewFromConfig_K8sRequiresInCluster(t *testing.T) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		t.Skip("running in-cluster; k8s backend would succeed")
	}
	_, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendK8s}, nil)
	assert.Error(t, err, "k8s backend out-of-cluster must error")
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	_, err := NewFromConfig(config.CoordinationConfig{Backend: "bogus"}, nil)
	assert.Error(t, err)
}
