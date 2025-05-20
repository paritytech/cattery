package repositories

import (
	"cattery/lib/maps"
	"cattery/lib/trays"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"strings"
)

var traysDictionary = maps.NewMongoSyncMap[string, trays.Tray]("id", true)

type MongodbTrayRepository struct {
	uri        string
	collection *mongo.Collection
}

func NewMongodbTrayRepository(uri string) *MongodbTrayRepository {
	return &MongodbTrayRepository{
		uri: uri,
	}
}

func (m MongodbTrayRepository) Connect(ctx context.Context) error {

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(m.uri).SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	m.collection = client.Database("cattery").Collection("trays")

	err = traysDictionary.Load(m.collection)
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbTrayRepository) Get(trayId string) (*trays.Tray, error) {
	return traysDictionary.Get(trayId), nil
}

func (m MongodbTrayRepository) Save(tray *trays.Tray) error {
	traysDictionary.Set(tray.Id(), tray)
	_, err := m.collection.InsertOne(context.Background(), tray)
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbTrayRepository) Delete(trayId string) error {
	traysDictionary.Delete(trayId)
	_, err := m.collection.DeleteOne(context.Background(), bson.M{"_id": trayId})
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbTrayRepository) Len() int {
	return traysDictionary.Len()
}

func (m MongodbTrayRepository) GetGroupByLabels() map[string][]*trays.Tray {
	groupedTrays := make(map[string][]*trays.Tray)

	for _, tray := range traysDictionary.GetAll() {
		var joinedLabels = strings.Join(tray.Labels(), ";")
		groupedTrays[joinedLabels] = append(groupedTrays[joinedLabels], tray)
	}

	return groupedTrays
}
