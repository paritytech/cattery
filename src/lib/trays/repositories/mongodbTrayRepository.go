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
			"status":        bson.M{"$nin": bson.A{trays.TrayStatusRunning, trays.TrayStatusDeleting}},
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

func (m *MongodbTrayRepository) MarkRedundant(ctx context.Context, trayType string, limit int) ([]*trays.Tray, error) {
	resultTrays := make([]*trays.Tray, 0, limit)

	for i := 0; i < limit; i++ {
		dbResult := m.collection.FindOneAndUpdate(
			ctx,
			bson.M{"status": trays.TrayStatusCreating, "trayTypeName": trayType},
			bson.M{"$set": bson.M{"status": trays.TrayStatusDeleting, "statusChanged": time.Now().UTC(), "jobRunId": 0}},
			options.FindOneAndUpdate().SetReturnDocument(options.After))

		var result trays.Tray
		err := dbResult.Decode(&result)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				break
			}
			return nil, err
		}

		resultTrays = append(resultTrays, &result)
	}

	return resultTrays, nil
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

func (m *MongodbTrayRepository) CountByTrayType(ctx context.Context, trayType string) (map[trays.TrayStatus]int, int, error) {
	matchStage := bson.D{
		{Key: "$match", Value: bson.D{{Key: "trayTypeName", Value: trayType}}},
	}
	groupStage := bson.D{
		{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$status"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}}

	cursor, err := m.collection.Aggregate(ctx, mongo.Pipeline{matchStage, groupStage})
	if err != nil {
		return nil, 0, err
	}

	var dbResults []bson.M
	if err = cursor.All(ctx, &dbResults); err != nil {
		return nil, 0, err
	}

	result := map[trays.TrayStatus]int{
		trays.TrayStatusCreating:    0,
		trays.TrayStatusRegistering: 0,
		trays.TrayStatusDeleting:    0,
		trays.TrayStatusRegistered:  0,
		trays.TrayStatusRunning:     0,
	}

	total := 0
	for _, res := range dbResults {
		status := res["_id"].(int32)
		cnt, _ := res["count"].(int32)
		result[trays.TrayStatus(status)] = int(cnt)
		total += int(cnt)
	}
	return result, total, nil
}
