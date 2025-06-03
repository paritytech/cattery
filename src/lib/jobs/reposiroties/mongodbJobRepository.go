package reposiroties

import (
	"cattery/lib/jobs"
	"cattery/lib/maps"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"strings"
)

var jobsDictionary = maps.NewMongoSyncMap[int64, jobs.Job]("id", true)

type MongodbJobRepository struct {
	IJobRepository
	uri        string
	collection *mongo.Collection
}

func NewMongodbJobRepository(uri string) *MongodbJobRepository {
	return &MongodbJobRepository{
		uri: uri,
	}
}

func (m MongodbJobRepository) Connect(ctx context.Context) error {

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(m.uri).SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	m.collection = client.Database("cattery").Collection("trays")

	err = jobsDictionary.Load(m.collection)
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbJobRepository) Get(jobId int64) (*jobs.Job, error) {
	return jobsDictionary.Get(jobId), nil
}

func (m MongodbJobRepository) Save(job *jobs.Job) error {
	jobsDictionary.Set(job.Id, job)
	_, err := m.collection.InsertOne(context.Background(), job)
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbJobRepository) Delete(jobId int64) error {
	jobsDictionary.Delete(jobId)
	_, err := m.collection.DeleteOne(context.Background(), bson.M{"_id": jobId})
	if err != nil {
		return err
	}

	return nil
}

func (m MongodbJobRepository) Len() int {
	return jobsDictionary.Len()
}

func (m MongodbJobRepository) GetGroupByLabels() map[string][]*jobs.Job {
	var allJobs = jobsDictionary.GetAll()

	// TODO move logic to map
	var groupedJobs = make(map[string][]*jobs.Job)
	for _, job := range allJobs {
		var joinedLabels = strings.Join(job.Labels, ";")
		groupedJobs[joinedLabels] = append(groupedJobs[joinedLabels], job)
	}

	return groupedJobs
}
