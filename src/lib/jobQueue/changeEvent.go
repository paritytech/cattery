package jobQueue

type changeEvent[T any] struct {
	OperationType string `bson:"operationType"`
	DocumentKey   struct {
		Id int64 `bson:"_id"`
	} `bson:"documentKey"`
	ns struct {
		db string
	} `bson:"ns"`
	FullDocument T `bson:"fullDocument"`
}
