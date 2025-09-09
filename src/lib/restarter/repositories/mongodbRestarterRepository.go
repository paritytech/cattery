package repositories

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	log "github.com/sirupsen/logrus"
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

func (m *MongodbRestarterRepository) SaveRestartRequest(workflowRunId int64) error {
	_, err := m.collection.UpdateOne(
		context.Background(),
		bson.M{
			"workflowRunId": workflowRunId,
		},
		bson.M{
			"$set": bson.M{
				"workflowRunId": workflowRunId,
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
	log.Debugf("Checking restart request for workflow run id %d in MongoDB", workflowRunId)
	dbResult := m.collection.FindOne(
		context.Background(),
		bson.M{
			"workflowRunId": workflowRunId,
		},
	)
	log.Debugf("MongoDB result: %+v", dbResult)
	var result bson.M
	err := dbResult.Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
