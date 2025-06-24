package jobQueue

import (
	"cattery/lib/jobs"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"testing"
)

// setupTestCollection creates a test collection and returns a client and collection
func setupTestCollection(t *testing.T) (*mongo.Client, *mongo.Collection) {
	t.Helper()

	// Connect to MongoDB
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatalf("Failed to connect to MongoDB: %v", err)
	}

	// Ping the database to verify connection
	err = client.Ping(context.Background(), nil)
	if err != nil {
		t.Fatalf("Failed to ping MongoDB: %v", err)
	}

	// Create a test collection
	collection := client.Database("test").Collection("jobs_test_queue_manager")

	// Clear the collection
	err = collection.Drop(context.Background())
	if err != nil {
		t.Fatalf("Failed to drop collection: %v", err)
	}

	return client, collection
}

// createTestJob creates a test job with the given parameters
func createTestJob(id int64, name string, trayType string) *jobs.Job {
	return &jobs.Job{
		Id:       id,
		Name:     name,
		TrayType: trayType,
	}
}

// insertTestJobs inserts test jobs into the collection
func insertTestJobs(t *testing.T, collection *mongo.Collection, jobs []*jobs.Job) {
	t.Helper()

	for _, job := range jobs {
		_, err := collection.InsertOne(context.Background(), job)
		if err != nil {
			t.Fatalf("Failed to insert test job: %v", err)
		}
	}
}

// TestNewQueueManager tests the NewQueueManager function
func TestNewQueueManager(t *testing.T) {
	// Test with listen=true
	qm := NewQueueManager(true)
	if qm == nil {
		t.Error("Expected non-nil QueueManager")
	}
	if qm.jobQueue == nil {
		t.Error("Expected non-nil jobQueue")
	}
	if !qm.listen {
		t.Error("Expected listen to be true")
	}

	// Test with listen=false
	qm = NewQueueManager(false)
	if qm == nil {
		t.Error("Expected non-nil QueueManager")
	}
	if qm.jobQueue == nil {
		t.Error("Expected non-nil jobQueue")
	}
	if qm.listen {
		t.Error("Expected listen to be false")
	}
}

// TestConnect tests the Connect method
func TestConnect(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	if qm.collection != collection {
		t.Error("Expected collection to be set")
	}
}

// TestLoad tests the Load method
func TestLoad(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test jobs
	job1 := createTestJob(1, "Test Job 1", "TestTray")
	job2 := createTestJob(2, "Test Job 2", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job1, job2})

	// Test Load with listen=false
	qm := NewQueueManager(false)
	qm.Connect(collection)
	err := qm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify jobs were loaded
	if qm.jobQueue.Get(1) == nil {
		t.Error("Expected job 1 to be loaded")
	}
	if qm.jobQueue.Get(2) == nil {
		t.Error("Expected job 2 to be loaded")
	}

	// Skip testing with listen=true in unit tests as it requires a running MongoDB replica set
	// In a real environment, this would be tested with a properly configured MongoDB replica set
	t.Log("Skipping test with listen=true as it requires a MongoDB replica set")
}

// TestAddJob tests the AddJob method
func TestAddJob(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	// Create a test job
	job := createTestJob(1, "Test Job", "TestTray")

	// Test AddJob
	err := qm.AddJob(job)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	// Verify job was added to the queue
	if qm.jobQueue.Get(1) == nil {
		t.Error("Expected job to be added to the queue")
	}

	// Verify job was added to the database
	var dbJob jobs.Job
	err = collection.FindOne(context.Background(), bson.M{"id": 1}).Decode(&dbJob)
	if err != nil {
		t.Fatalf("Failed to find job in database: %v", err)
	}

	if dbJob.Id != 1 {
		t.Errorf("Expected job ID 1, got %d", dbJob.Id)
	}
	if dbJob.Name != "Test Job" {
		t.Errorf("Expected job name 'Test Job', got '%s'", dbJob.Name)
	}
	if dbJob.TrayType != "TestTray" {
		t.Errorf("Expected tray type 'TestTray', got '%s'", dbJob.TrayType)
	}
}

// TestJobInProgress tests the JobInProgress method
func TestJobInProgress(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	// Create and add a test job
	job := createTestJob(1, "Test Job", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job})
	qm.jobQueue.Add(job)

	// Test JobInProgress
	err := qm.JobInProgress(1)
	if err != nil {
		t.Fatalf("JobInProgress failed: %v", err)
	}

	// Verify job was removed from the queue
	if qm.jobQueue.Get(1) != nil {
		t.Error("Expected job to be removed from the queue")
	}

	// Verify job was removed from the database
	count, err := collection.CountDocuments(context.Background(), bson.M{"id": 1})
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 jobs in database, got %d", count)
	}

	// Test JobInProgress with non-existent job
	err = qm.JobInProgress(999)
	if err == nil {
		t.Error("Expected error for non-existent job, got nil")
	}
}

// TestUpdateJobStatus tests the UpdateJobStatus method
func TestUpdateJobStatus(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	// Create and add a test job
	job := createTestJob(1, "Test Job", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job})
	qm.jobQueue.Add(job)

	// Test UpdateJobStatus with JobStatusInProgress
	err := qm.UpdateJobStatus(1, jobs.JobStatusInProgress)
	if err != nil {
		t.Fatalf("UpdateJobStatus failed: %v", err)
	}

	// Verify job was removed from the queue
	if qm.jobQueue.Get(1) != nil {
		t.Error("Expected job to be removed from the queue")
	}

	// Verify job was removed from the database
	count, err := collection.CountDocuments(context.Background(), bson.M{"id": 1})
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 jobs in database, got %d", count)
	}

	// Add the job back for the next test
	job = createTestJob(1, "Test Job", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job})
	qm.jobQueue.Add(job)

	// Test UpdateJobStatus with JobStatusFinished
	err = qm.UpdateJobStatus(1, jobs.JobStatusFinished)
	if err != nil {
		t.Fatalf("UpdateJobStatus failed: %v", err)
	}

	// Verify job was removed from the queue
	if qm.jobQueue.Get(1) != nil {
		t.Error("Expected job to be removed from the queue")
	}

	// Verify job was removed from the database
	count, err = collection.CountDocuments(context.Background(), bson.M{"id": 1})
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 jobs in database, got %d", count)
	}

	// Add the job back for the next test
	job = createTestJob(1, "Test Job", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job})
	qm.jobQueue.Add(job)

	// Test UpdateJobStatus with other status (should do nothing)
	err = qm.UpdateJobStatus(1, jobs.JobStatusQueued)
	if err != nil {
		t.Fatalf("UpdateJobStatus failed: %v", err)
	}

	// Verify job is still in the queue
	if qm.jobQueue.Get(1) == nil {
		t.Error("Expected job to still be in the queue")
	}

	// Verify job is still in the database
	count, err = collection.CountDocuments(context.Background(), bson.M{"id": 1})
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 job in database, got %d", count)
	}

	// Test UpdateJobStatus with non-existent job
	err = qm.UpdateJobStatus(999, jobs.JobStatusInProgress)
	if err == nil {
		t.Error("Expected error for non-existent job, got nil")
	}
}

// TestDeleteJob tests the deleteJob method indirectly through JobInProgress
func TestDeleteJob(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	// Create and add a test job
	job := createTestJob(1, "Test Job", "TestTray")
	insertTestJobs(t, collection, []*jobs.Job{job})
	qm.jobQueue.Add(job)

	// Test deleteJob through JobInProgress
	err := qm.JobInProgress(1)
	if err != nil {
		t.Fatalf("JobInProgress failed: %v", err)
	}

	// Verify job was removed from the queue
	if qm.jobQueue.Get(1) != nil {
		t.Error("Expected job to be removed from the queue")
	}

	// Verify job was removed from the database
	count, err := collection.CountDocuments(context.Background(), bson.M{"id": 1})
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 jobs in database, got %d", count)
	}
}

// TestQueueManagerGetJobsCount tests the GetJobsCount method
func TestQueueManagerGetJobsCount(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	qm := NewQueueManager(false)
	qm.Connect(collection)

	// Test with empty queue
	counts := qm.GetJobsCount()
	if len(counts) != 0 {
		t.Errorf("Expected empty counts map for empty queue, got %d items", len(counts))
	}

	// Add some jobs
	job1 := createTestJob(1, "Test Job 1", "TestTray1")
	job2 := createTestJob(2, "Test Job 2", "TestTray1")
	job3 := createTestJob(3, "Test Job 3", "TestTray2")

	qm.jobQueue.Add(job1)
	qm.jobQueue.Add(job2)
	qm.jobQueue.Add(job3)

	// Test with populated queue
	counts = qm.GetJobsCount()

	if len(counts) != 2 {
		t.Errorf("Expected 2 items in counts map, got %d", len(counts))
	}

	if counts["TestTray1"] != 2 {
		t.Errorf("Expected 2 jobs in TestTray1, got %d", counts["TestTray1"])
	}

	if counts["TestTray2"] != 1 {
		t.Errorf("Expected 1 job in TestTray2, got %d", counts["TestTray2"])
	}
}
