//go:build integration_mongo

package election

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func setupLeaseCollection(t *testing.T) *mongo.Collection {
	t.Helper()

	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://localhost"))
	require.NoError(t, err)
	require.NoError(t, client.Ping(context.Background(), nil))

	coll := client.Database("test").Collection("leases_test")
	require.NoError(t, coll.Drop(context.Background()))

	t.Cleanup(func() {
		_ = coll.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return coll
}

// Exercises the full lease protocol against a real Mongo: acquire, renew,
// contention, expiry takeover, and holder-scoped release.
func TestMongoLeaseStore_Lifecycle(t *testing.T) {
	store := NewMongoLeaseStore()
	store.Connect(setupLeaseCollection(t))

	ctx := context.Background()
	const ttl = 200 * time.Millisecond

	// acquire a free key
	ok, err := store.Acquire(ctx, "k", "A", ttl)
	require.NoError(t, err)
	assert.True(t, ok, "acquire free key")

	// same holder renews
	ok, err = store.Acquire(ctx, "k", "A", ttl)
	require.NoError(t, err)
	assert.True(t, ok, "holder renews own lease")

	// another holder cannot take a live lease
	ok, err = store.Acquire(ctx, "k", "B", ttl)
	require.NoError(t, err)
	assert.False(t, ok, "live lease blocks other holders")

	// after expiry, B takes over
	time.Sleep(ttl + 50*time.Millisecond)
	ok, err = store.Acquire(ctx, "k", "B", ttl)
	require.NoError(t, err)
	assert.True(t, ok, "expired lease can be taken over")

	// A is no longer the holder and cannot renew
	ok, err = store.Acquire(ctx, "k", "A", ttl)
	require.NoError(t, err)
	assert.False(t, ok, "former holder cannot renew")

	// release by a non-holder is a no-op
	require.NoError(t, store.Release(ctx, "k", "A"))
	ok, err = store.Acquire(ctx, "k", "C", ttl)
	require.NoError(t, err)
	assert.False(t, ok, "non-holder release must not free the lease")

	// release by the holder frees it
	require.NoError(t, store.Release(ctx, "k", "B"))
	ok, err = store.Acquire(ctx, "k", "C", ttl)
	require.NoError(t, err)
	assert.True(t, ok, "holder release frees the lease")
}
