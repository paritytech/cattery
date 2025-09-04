package maps

import (
	"context"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sync"
)

type changeEvent[T any] struct {
	OperationType string `bson:"operationType"`
	FullDocument  T      `bson:"fullDocument"`
}

type MongoSyncMap[T comparable, Y any] struct {
	_map       *ConcurrentMap[T, Y]
	collection *mongo.Collection
	idField    string
	listen     bool

	changeStream *mongo.ChangeStream
	waitGroup    *sync.WaitGroup
}

func NewMongoSyncMap[T comparable, Y any](idField string, listen bool) *MongoSyncMap[T, Y] {
	return &MongoSyncMap[T, Y]{
		_map:      NewConcurrentMap[T, Y](),
		idField:   idField,
		listen:    listen,
		waitGroup: &sync.WaitGroup{},
	}
}

func (m *MongoSyncMap[T, Y]) Load(collection *mongo.Collection) error {

	m.waitGroup.Add(1)
	defer m.waitGroup.Done()

	m.collection = collection

	if m.listen {
		changeStream, err := m.collection.Watch(nil, mongo.Pipeline{})
		if err != nil {
			return err
		}
		m.changeStream = changeStream
	}

	allTrays, err := m.collection.Find(nil, bson.M{})
	if err != nil {
		return err
	}

	for allTrays.Next(nil) {
		var tray Y
		decodeErr := allTrays.Decode(&tray)
		if decodeErr != nil {
			return err
		}

		var id T
		err := allTrays.Current.Lookup(m.idField).Unmarshal(&id)
		if err != nil {
			return err
		}
		m._map.Set(id, &tray)
	}

	if m.listen {
		go func() {
			for m.changeStream.Next(nil) {
				var event changeEvent[Y]
				decodeErr := m.changeStream.Decode(&event)
				if decodeErr != nil {
					log.Error("Failed to decode change stream: ", decodeErr)
					m.Load(collection)
				}

				var id T
				err := m.changeStream.Current.Lookup("fullDocument", m.idField).Unmarshal(&id)
				if err != nil {
					panic(err)
				}

				switch event.OperationType {
				case "replace":
					fallthrough
				case "update":
					fallthrough
				case "insert":
					m._map.Set(id, &event.FullDocument)
				case "delete":
					m._map.Delete(id)
				default:
					log.Warn("Unknown operation type: ", event.OperationType)
				}
			}
		}()
	}

	return nil
}

func (m *MongoSyncMap[T, Y]) Stop() error {
	if m.listen {
		err := m.changeStream.Close(nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *MongoSyncMap[T, Y]) Get(key T) *Y {
	m.waitGroup.Wait()
	return m._map.Get(key)
}

func (m *MongoSyncMap[T, Y]) Set(key T, value *Y) error {
	m.waitGroup.Wait()

	_, err := m.collection.UpdateOne(context.Background(), bson.M{m.idField: key}, value, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}

	m._map.Set(key, value)
	return nil
}

func (m *MongoSyncMap[T, Y]) Delete(key T) error {
	m.waitGroup.Wait()

	_, err := m.collection.DeleteOne(context.Background(), bson.M{m.idField: key})
	if err != nil {
		return err
	}

	m._map.Delete(key)
	return nil
}

func (m *MongoSyncMap[T, Y]) Len() int {
	m.waitGroup.Wait()

	return m._map.Len()
}

func (m *MongoSyncMap[T, Y]) GetAll() map[T]*Y {
	m.waitGroup.Wait()
	return m._map._map
}
