package election

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoLeaseStore implements LeaseStore on a MongoDB collection. One document
// per key, _id = key. It relies on Mongo's per-document atomicity and the
// implicit unique index on _id — no extra index or schema setup is required.
type MongoLeaseStore struct {
	collection *mongo.Collection
}

func NewMongoLeaseStore() *MongoLeaseStore {
	return &MongoLeaseStore{}
}

func (s *MongoLeaseStore) Connect(collection *mongo.Collection) {
	s.collection = collection
}

// Acquire upserts the lease document only when it is free to take. The filter
// matches when the lease is already ours (renew) or expired (takeover); with
// upsert enabled, a non-match means a live lease is held by someone else, which
// Mongo surfaces as a duplicate-key error on the _id insert — that is the
// "someone else leads" signal, not a real failure.
//
// Concurrent takeovers of an expired lease are safe: Mongo serializes the
// document writes, the first wins, and the loser's filter no longer matches the
// now-future expiresAt, so it falls through to the duplicate-key path.
func (s *MongoLeaseStore) Acquire(ctx context.Context, key, holder string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()

	filter := bson.M{
		"_id": key,
		"$or": bson.A{
			bson.M{"holder": holder},
			bson.M{"expiresAt": bson.M{"$lt": now}},
		},
	}
	update := bson.M{
		"$set": bson.M{
			"holder":    holder,
			"expiresAt": now.Add(ttl),
		},
	}

	_, err := s.collection.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return false, nil // live lease held by another replica
		}
		return false, err
	}
	return true, nil
}

func (s *MongoLeaseStore) Release(ctx context.Context, key, holder string) error {
	_, err := s.collection.DeleteOne(ctx, bson.M{"_id": key, "holder": holder})
	return err
}
