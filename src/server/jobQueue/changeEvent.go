package jobQueue

type changeEvent[T any] struct {
	OperationType string `bson:"operationType"`
	FullDocument  T      `bson:"fullDocument"`
}
