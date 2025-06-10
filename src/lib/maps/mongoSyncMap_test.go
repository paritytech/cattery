package maps

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"testing"
	"time"
)

type Obj struct {
	Id   string
	Name string
}

func init() {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	var collection = client.Database("test").Collection("test")
	collection.Drop(context.Background())

	collection.InsertOne(context.Background(), Obj{Id: "1", Name: "test"})
	collection.InsertOne(context.Background(), Obj{Id: "2", Name: "test2"})
	collection.InsertOne(context.Background(), Obj{Id: "3", Name: "test3"})
	collection.InsertOne(context.Background(), Obj{Id: "4", Name: "test4"})
	collection.InsertOne(context.Background(), Obj{Id: "5", Name: "test5"})
}

func TestConnectLoad(t *testing.T) {

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	var collection = client.Database("test").Collection("test")

	var msm = NewMongoSyncMap[string, Obj]("id", false)

	msm.Load(collection)

	if msm.Len() != 5 {
		t.Errorf("Expected 5, got %d", msm.Len())
	}
}

func TestListen(t *testing.T) {

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	var collection = client.Database("test").Collection("test")

	var msm = NewMongoSyncMap[string, Obj]("id", true)
	msm.Load(collection)

	collection.InsertOne(context.Background(), Obj{Id: "6", Name: "test6"})
	collection.InsertOne(context.Background(), Obj{Id: "7", Name: "test7"})
	collection.InsertOne(context.Background(), Obj{Id: "8", Name: "test8"})

	time.Sleep(1 * time.Second)

	if msm.Len() != 8 {
		t.Errorf("Expected 8, got %d", msm.Len())
	}
}

func TestListenMultiple(t *testing.T) {

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	var collection = client.Database("test").Collection("test")

	var msm1 = NewMongoSyncMap[string, Obj]("id", true)
	msm1.Load(collection)

	var msm2 = NewMongoSyncMap[string, Obj]("id", true)
	msm2.Load(collection)

	msm1.Set("6", &Obj{Id: "6", Name: "test6"})
	msm1.Set("7", &Obj{Id: "7", Name: "test7"})
	msm1.Set("8", &Obj{Id: "8", Name: "test8"})

	time.Sleep(1 * time.Second)

	if msm1.Len() != 8 {
		t.Errorf("Expected 8, got %d", msm1.Len())
	}

	if msm2.Len() != 8 {
		t.Errorf("Expected 8, got %d", msm2.Len())
	}
}
