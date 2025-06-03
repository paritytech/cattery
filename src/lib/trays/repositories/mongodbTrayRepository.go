package repositories

import (
	"cattery/lib/trays"
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"time"
)

type MongodbTrayRepository struct {
	uri        string
	collection *mongo.Collection
}

func NewMongodbTrayRepository() *MongodbTrayRepository {
	return &MongodbTrayRepository{}
}

func (m *MongodbTrayRepository) Connect(collection *mongo.Collection) {
	m.collection = collection
}

func (m *MongodbTrayRepository) GetById(trayId string) (*trays.Tray, error) {
	dbResult := m.collection.FindOne(context.Background(), bson.M{"trayId": trayId})

	var result trays.Tray
	err := dbResult.Decode(&result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (m *MongodbTrayRepository) GetByJobRunId(jobRunId int64) (*trays.Tray, error) {
	dbResult := m.collection.FindOne(context.Background(), bson.M{"jobRunId": jobRunId})

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

func (m *MongodbTrayRepository) Save(tray *trays.Tray) error {
	tray.StatusChanged = time.Now().UTC()
	_, err := m.collection.InsertOne(context.Background(), tray)
	if err != nil {
		return err
	}

	return nil
}

func (m *MongodbTrayRepository) UpdateStatus(trayId string, status trays.TrayStatus, jobRunId int64) (*trays.Tray, error) {

	dbResult := m.collection.FindOneAndUpdate(
		context.Background(),
		bson.M{"id": trayId},
		bson.M{"$set": bson.M{"status": status, "statusChanged": time.Now().UTC(), "jobRunId": jobRunId}},
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

func (m *MongodbTrayRepository) Delete(trayId string) error {
	_, err := m.collection.DeleteOne(context.Background(), bson.M{"_id": trayId})
	if err != nil {
		return err
	}

	return nil
}

func (m *MongodbTrayRepository) CountByTrayType(trayType string) (int, error) {
	count, err := m.collection.CountDocuments(context.Background(), bson.M{"trayType": trayType, "status": bson.M{"$ne": trays.TrayStatusDeleting}})
	if err != nil {
		return 0, err
	}

	return int(count), nil
}
