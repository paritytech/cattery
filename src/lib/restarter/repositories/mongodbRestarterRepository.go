package repositories

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongodbRestarterRepository struct {
	collection *mongo.Collection
}

func NewMongodbRestarterRepository() *MongodbRestarterRepository {
	return &MongodbRestarterRepository{}
}

func (m *MongodbRestarterRepository) Connect(collection *mongo.Collection) {
	m.collection = collection
}

func (m *MongodbRestarterRepository) SaveRestartRequest(workflowRunId int64, orgName string, repoName string) error {
	_, err := m.collection.UpdateOne(
		context.Background(),
		bson.M{
			"workflowRunId": workflowRunId,
		},
		bson.M{
			"$set": bson.M{
				"workflowRunId": workflowRunId,
				"orgName":       orgName,
				"repoName":      repoName,
				"createdAt":     time.Now().UTC(),
			},
		},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (m *MongodbRestarterRepository) DeleteRestartRequest(workflowRunId int64) error {
	_, err := m.collection.DeleteOne(
		context.Background(),
		bson.M{
			"workflowRunId": workflowRunId,
		},
	)
	return err
}

func (m *MongodbRestarterRepository) CheckRestartRequest(workflowRunId int64) (bool, error) {
	dbResult := m.collection.FindOne(
		context.Background(),
		bson.M{
			"workflowRunId": workflowRunId,
		},
	)
	var result bson.M
	err := dbResult.Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (m *MongodbRestarterRepository) GetAllPendingRestartRequests() ([]RestartRequest, error) {
	cursor, err := m.collection.Find(context.Background(), bson.M{})
	if err != nil {
		return nil, err
	}

	var requests []RestartRequest
	if err := cursor.All(context.Background(), &requests); err != nil {
		return nil, err
	}
	return requests, nil
}
