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

func (m *MongodbTrayRepository) GetById(trayId string) (*trays.Tray, error) {
	dbResult := m.collection.FindOne(context.Background(), bson.M{"id": trayId})

	var result trays.Tray
	err := dbResult.Decode(&result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (m *MongodbTrayRepository) GetStale(d time.Duration, rd time.Duration) ([]*trays.Tray, error) {
	dbResult, err := m.collection.Find(context.Background(),
		bson.M{"$or": []bson.M{
			{
				"status":        bson.M{"$ne": trays.TrayStatusRunning},
				"statusChanged": bson.M{"$lte": time.Now().UTC().Add(-d)},
			},
		},
		})
	if err != nil {
		return nil, err
	}

	var traysArr []*trays.Tray
	if err := dbResult.All(context.Background(), &traysArr); err != nil {
		return nil, err
	}
	return traysArr, nil

}

func (m *MongodbTrayRepository) MarkRedundant(trayType string, limit int) ([]*trays.Tray, error) {

	var resultTrays = make([]*trays.Tray, 0)
	var ids = make([]string, 0)

	for i := 0; i < limit; i++ {
		dbResult := m.collection.FindOneAndUpdate(
			context.Background(),
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
		ids = append(ids, result.Id)
	}

	return resultTrays, nil
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

func (m *MongodbTrayRepository) UpdateStatus(trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64) (*trays.Tray, error) {

	var setQuery = bson.M{"status": status, "statusChanged": time.Now().UTC()}

	if jobRunId != 0 {
		setQuery["jobRunId"] = jobRunId
	}

	if ghRunnerId != 0 {
		setQuery["gitHubRunnerId"] = ghRunnerId
	}

	if workflowRunId != 0 {
		setQuery["workflowRunId"] = workflowRunId
	}

	dbResult := m.collection.FindOneAndUpdate(
		context.Background(),
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

func (m *MongodbTrayRepository) Delete(trayId string) error {
	_, err := m.collection.DeleteOne(context.Background(), bson.M{"id": trayId})
	if err != nil {
		return err
	}

	return nil
}

func (m *MongodbTrayRepository) CountByTrayType(trayType string) (map[trays.TrayStatus]int, int, error) {

	var matchStage = bson.D{
		{"$match", bson.D{{"trayTypeName", trayType}}},
	}
	var groupStage = bson.D{
		{"$group", bson.D{
			{"_id", "$status"},
			{"count", bson.D{{"$sum", 1}}},
		}}}

	cursor, err := m.collection.Aggregate(context.Background(), mongo.Pipeline{matchStage, groupStage})
	if err != nil {
		return nil, 0, err
	}

	var dbResults []bson.M
	if err = cursor.All(context.TODO(), &dbResults); err != nil {
		return nil, 0, err
	}

	var result = make(map[trays.TrayStatus]int)
	result[trays.TrayStatusCreating] = 0
	result[trays.TrayStatusRegistering] = 0
	result[trays.TrayStatusDeleting] = 0
	result[trays.TrayStatusRegistered] = 0
	result[trays.TrayStatusRunning] = 0

	var total = 0

	for _, res := range dbResults {
		var int32Status = res["_id"].(int32)

		status := int32Status
		cnt, _ := res["count"].(int32)
		result[trays.TrayStatus(status)] = int(cnt)
		total += int(cnt)
	}
	return result, total, nil

}
