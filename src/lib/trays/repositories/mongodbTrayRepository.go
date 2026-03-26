package repositories

import (
	"cattery/lib/trays"
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongodbTrayRepository struct {
	collection *mongo.Collection
}

func NewMongodbTrayRepository() *MongodbTrayRepository {
	return &MongodbTrayRepository{}
}

func (m *MongodbTrayRepository) Connect(collection *mongo.Collection) {
	m.collection = collection
}

func (m *MongodbTrayRepository) GetById(ctx context.Context, trayId string) (*trays.Tray, error) {
	dbResult := m.collection.FindOne(ctx, bson.M{"id": trayId})

	var result trays.Tray
	err := dbResult.Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	return &result, nil
}

func (m *MongodbTrayRepository) GetStale(ctx context.Context, d time.Duration) ([]*trays.Tray, error) {
	dbResult, err := m.collection.Find(ctx,
		bson.M{
			"status":        bson.M{"$ne": trays.TrayStatusRunning},
			"statusChanged": bson.M{"$lte": time.Now().UTC().Add(-d)},
		})
	if err != nil {
		return nil, err
	}

	var traysArr []*trays.Tray
	if err := dbResult.All(ctx, &traysArr); err != nil {
		return nil, err
	}
	return traysArr, nil
}

func (m *MongodbTrayRepository) CountActive(ctx context.Context, trayType string) (int, error) {
	count, err := m.collection.CountDocuments(ctx, bson.M{
		"trayTypeName": trayType,
		"status":       bson.M{"$ne": trays.TrayStatusDeleting},
	})
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func (m *MongodbTrayRepository) Save(ctx context.Context, tray *trays.Tray) error {
	tray.StatusChanged = time.Now().UTC()
	_, err := m.collection.InsertOne(ctx, tray)
	return err
}

func (m *MongodbTrayRepository) UpdateStatus(ctx context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string) (*trays.Tray, error) {
	setQuery := bson.M{"status": status, "statusChanged": time.Now().UTC()}

	if jobRunId != 0 {
		setQuery["jobRunId"] = jobRunId
	}
	if ghRunnerId != 0 {
		setQuery["gitHubRunnerId"] = ghRunnerId
	}
	if workflowRunId != 0 {
		setQuery["workflowRunId"] = workflowRunId
	}
	if repository != "" {
		setQuery["repository"] = repository
	}

	dbResult := m.collection.FindOneAndUpdate(
		ctx,
		bson.M{"id": trayId},
		bson.M{"$set": setQuery},
		options.FindOneAndUpdate().SetReturnDocument(options.After))

	var result trays.Tray
	err := dbResult.Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	return &result, nil
}

func (m *MongodbTrayRepository) Delete(ctx context.Context, trayId string) error {
	_, err := m.collection.DeleteOne(ctx, bson.M{"id": trayId})
	return err
}

