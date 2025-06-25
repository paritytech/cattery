package repositories

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"reflect"
	"testing"
	"time"
)

// TestTray is a helper struct to create test trays
type TestTray struct {
	Id            string           `bson:"id"`
	TrayType      string           `bson:"trayType"`
	GitHubOrgName string           `bson:"gitHubOrgName"`
	JobRunId      int64            `bson:"jobRunId"`
	Status        trays.TrayStatus `bson:"status"`
	StatusChanged time.Time        `bson:"statusChanged"`
}

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
	collection := client.Database("test").Collection("trays_test")

	// Clear the collection
	err = collection.Drop(context.Background())
	if err != nil {
		t.Fatalf("Failed to drop collection: %v", err)
	}

	return client, collection
}

// createTestTray creates a test tray with the given parameters
func createTestTray(id string, trayType string, status trays.TrayStatus, jobRunId int64) *TestTray {
	return &TestTray{
		Id:            id,
		TrayType:      trayType,
		GitHubOrgName: "test-org",
		JobRunId:      jobRunId,
		Status:        status,
		StatusChanged: time.Now().UTC(),
	}
}

// insertTestTrays inserts test trays into the collection
func insertTestTrays(t *testing.T, collection *mongo.Collection, trays []*TestTray) {
	t.Helper()

	for _, tray := range trays {
		_, err := collection.InsertOne(context.Background(), tray)
		if err != nil {
			t.Fatalf("Failed to insert test tray: %v", err)
		}
	}
}

// TestGetById tests the GetById method
func TestGetById(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data
	testTray := createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray})

	// Test GetById
	tray, err := repo.GetById("test-tray-1")
	if err != nil {
		t.Fatalf("GetById failed: %v", err)
	}

	if tray == nil {
		t.Fatal("GetById returned nil tray")
	}

	if tray.Id != "test-tray-1" {
		t.Errorf("Expected tray ID 'test-tray-1', got '%s'", tray.Id)
	}

	if tray.TrayType != "test-type" {
		t.Errorf("Expected tray type 'test-type', got '%s'", tray.TrayType)
	}

	if tray.Status != trays.TrayStatusCreating {
		t.Errorf("Expected tray status %v, got %v", trays.TrayStatusCreating, tray.Status)
	}

	// Test GetById with non-existent ID
	tray, err = repo.GetById("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent tray, got nil")
	}

}

// TestSave tests the Save method
func TestSave(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Create a tray to save
	trayType := config.TrayType{
		Name:          "test-type",
		Provider:      "test-provider",
		RunnerGroupId: 123,
		GitHubOrg:     "test-org",
		Config:        config.TrayConfig{},
	}

	tray := trays.NewTray(trayType)

	// Test Save
	err := repo.Save(tray)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify the tray was saved
	savedTray, err := repo.GetById(tray.Id)
	if err != nil {
		t.Fatalf("Failed to get saved tray: %v", err)
	}

	if savedTray == nil {
		t.Fatal("GetById returned nil for saved tray")
	}

	if savedTray.Id != tray.Id {
		t.Errorf("Expected saved tray ID '%s', got '%s'", tray.Id, savedTray.Id)
	}

	if savedTray.TrayType != tray.TrayType {
		t.Errorf("Expected saved tray type '%s', got '%s'", tray.TrayType, savedTray.TrayType)
	}

	if savedTray.Status != tray.Status {
		t.Errorf("Expected saved tray status %v, got %v", tray.Status, savedTray.Status)
	}
}

// TestUpdateStatus tests the UpdateStatus method
func TestUpdateStatus(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data
	testTray := createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray})

	// Test UpdateStatus with jobRunId only
	updatedTray, err := repo.UpdateStatus("test-tray-1", trays.TrayStatusRegistered, 123, 0)
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	if updatedTray == nil {
		t.Fatal("UpdateStatus returned nil tray")
	}

	if updatedTray.Status != trays.TrayStatusRegistered {
		t.Errorf("Expected updated status %v, got %v", trays.TrayStatusRegistered, updatedTray.Status)
	}

	if updatedTray.JobRunId != 123 {
		t.Errorf("Expected updated JobRunId 123, got %d", updatedTray.JobRunId)
	}

	// Test UpdateStatus with ghRunnerId
	updatedTray, err = repo.UpdateStatus("test-tray-1", trays.TrayStatusRunning, 456, 789)
	if err != nil {
		t.Fatalf("UpdateStatus with ghRunnerId failed: %v", err)
	}

	if updatedTray == nil {
		t.Fatal("UpdateStatus returned nil tray")
	}

	if updatedTray.Status != trays.TrayStatusRunning {
		t.Errorf("Expected updated status %v, got %v", trays.TrayStatusRunning, updatedTray.Status)
	}

	if updatedTray.JobRunId != 456 {
		t.Errorf("Expected updated JobRunId 456, got %d", updatedTray.JobRunId)
	}

	if updatedTray.GitHubRunnerId != 789 {
		t.Errorf("Expected updated GitHubRunnerId 789, got %d", updatedTray.GitHubRunnerId)
	}

	// Test UpdateStatus with non-existent ID
	updatedTray, err = repo.UpdateStatus("non-existent", trays.TrayStatusRegistered, 123, 0)
	if err != nil {
		t.Fatalf("UpdateStatus with non-existent ID failed: %v", err)
	}

	if updatedTray != nil {
		t.Error("Expected nil tray for non-existent ID, got non-nil")
	}
}

// TestDelete tests the Delete method
func TestDelete(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data
	testTray := createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray})

	// Test Delete
	err := repo.Delete("test-tray-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify the tray was deleted
	deletedTray, err := repo.GetById("test-tray-1")
	if err == nil {
		t.Error("Expected error for deleted tray, got nil")
	}

	if deletedTray != nil {
		t.Error("Expected nil for deleted tray, got non-nil")
	}

	// Test Delete with non-existent ID
	err = repo.Delete("non-existent")
	if err != nil {
		t.Fatalf("Delete with non-existent ID failed: %v", err)
	}
}

// TestGetByJobRunId tests the GetByJobRunId method
func TestGetByJobRunId(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data
	testTray1 := createTestTray("test-tray-1", "test-type", trays.TrayStatusRunning, 123)
	testTray2 := createTestTray("test-tray-2", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray1, testTray2})

	// Test GetByJobRunId
	tray, err := repo.GetByJobRunId(123)
	if err != nil {
		t.Fatalf("GetByJobRunId failed: %v", err)
	}

	if tray == nil {
		t.Fatal("GetByJobRunId returned nil tray")
	}

	if tray.Id != "test-tray-1" {
		t.Errorf("Expected tray ID 'test-tray-1', got '%s'", tray.Id)
	}

	if tray.JobRunId != 123 {
		t.Errorf("Expected JobRunId 123, got %d", tray.JobRunId)
	}

	// Test GetByJobRunId with non-existent JobRunId
	tray, err = repo.GetByJobRunId(999)
	if err != nil {
		t.Fatalf("GetByJobRunId with non-existent JobRunId failed: %v", err)
	}

	if tray != nil {
		t.Error("Expected nil tray for non-existent JobRunId, got non-nil")
	}
}

// TestMarkRedundant tests the MarkRedundant method
func TestMarkRedundant(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data
	testTray1 := createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0)
	testTray2 := createTestTray("test-tray-2", "test-type", trays.TrayStatusCreating, 0)
	testTray3 := createTestTray("test-tray-3", "test-type", trays.TrayStatusRegistered, 0)
	testTray4 := createTestTray("test-tray-4", "other-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray1, testTray2, testTray3, testTray4})

	// Test MarkRedundant
	redundantTrays, err := repo.MarkRedundant("test-type", 2)
	if err != nil {
		t.Fatalf("MarkRedundant failed: %v", err)
	}

	// Verify that the correct number of trays were marked as redundant
	if len(redundantTrays) != 2 {
		t.Errorf("Expected 2 redundant trays, got %d", len(redundantTrays))
	}

	// Verify that the trays were actually marked as deleting in the database
	// by querying the database directly
	cursor, err := collection.Find(context.Background(), bson.M{"trayType": "test-type", "status": trays.TrayStatusDeleting})
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}

	var deletingTrays []TestTray
	err = cursor.All(context.Background(), &deletingTrays)
	if err != nil {
		t.Fatalf("Failed to decode cursor: %v", err)
	}

	if len(deletingTrays) != 2 {
		t.Errorf("Expected 2 trays marked as deleting in the database, got %d", len(deletingTrays))
	}

	// Verify that the correct trays were marked as deleting
	deletingTrayIds := make(map[string]bool)
	for _, tray := range deletingTrays {
		deletingTrayIds[tray.Id] = true

		// Verify the status and jobRunId were updated correctly
		if tray.Status != trays.TrayStatusDeleting {
			t.Errorf("Expected tray status %v, got %v", trays.TrayStatusDeleting, tray.Status)
		}

		if tray.JobRunId != 0 {
			t.Errorf("Expected JobRunId 0, got %d", tray.JobRunId)
		}
	}

	// Check that the correct trays were marked as deleting
	if !deletingTrayIds["test-tray-1"] {
		t.Error("Expected test-tray-1 to be marked as deleting")
	}

	if !deletingTrayIds["test-tray-2"] {
		t.Error("Expected test-tray-2 to be marked as deleting")
	}

	// Verify that trays with different status or type were not affected
	unchangedTray, err := repo.GetById("test-tray-3")
	if err != nil {
		t.Fatalf("Failed to get test-tray-3: %v", err)
	}

	if unchangedTray.Status != trays.TrayStatusRegistered {
		t.Errorf("Expected test-tray-3 status to remain %v, got %v", trays.TrayStatusRegistered, unchangedTray.Status)
	}

	unchangedTray, err = repo.GetById("test-tray-4")
	if err != nil {
		t.Fatalf("Failed to get test-tray-4: %v", err)
	}

	if unchangedTray.Status != trays.TrayStatusCreating {
		t.Errorf("Expected test-tray-4 status to remain %v, got %v", trays.TrayStatusCreating, unchangedTray.Status)
	}

	// Test MarkRedundant with limit
	// Add more test trays
	testTray5 := createTestTray("test-tray-5", "test-type", trays.TrayStatusCreating, 0)
	testTray6 := createTestTray("test-tray-6", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray5, testTray6})

	// Mark only 1 tray as redundant
	redundantTrays, err = repo.MarkRedundant("test-type", 1)
	if err != nil {
		t.Fatalf("MarkRedundant with limit failed: %v", err)
	}

	// Verify that only 1 more tray was marked as deleting
	cursor, err = collection.Find(context.Background(), bson.M{"trayType": "test-type", "status": trays.TrayStatusDeleting})
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}

	err = cursor.All(context.Background(), &deletingTrays)
	if err != nil {
		t.Fatalf("Failed to decode cursor: %v", err)
	}

	if len(deletingTrays) != 3 {
		t.Errorf("Expected 3 trays marked as deleting in the database, got %d", len(deletingTrays))
	}

	// Test MarkRedundant with non-existent tray type
	redundantTrays, err = repo.MarkRedundant("non-existent", 2)
	if err != nil {
		t.Fatalf("MarkRedundant with non-existent tray type failed: %v", err)
	}

	if len(redundantTrays) != 0 {
		t.Errorf("Expected 0 redundant trays for non-existent type, got %d", len(redundantTrays))
	}

	// Test MarkRedundant with empty collection
	// Clear the collection
	err = collection.Drop(context.Background())
	if err != nil {
		t.Fatalf("Failed to drop collection: %v", err)
	}

	// Try to mark redundant trays in an empty collection
	redundantTrays, err = repo.MarkRedundant("test-type", 2)
	if err != nil {
		t.Fatalf("MarkRedundant with empty collection failed: %v", err)
	}

	if len(redundantTrays) != 0 {
		t.Errorf("Expected 0 redundant trays for empty collection, got %d", len(redundantTrays))
	}
}

// TestGetStale tests the GetStale method
func TestGetStale(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Create test trays with different statusChanged timestamps
	// Stale trays (older than 5 minutes)
	staleTray1 := createTestTray("stale-tray-1", "test-type", trays.TrayStatusCreating, 0)
	staleTray1.StatusChanged = time.Now().UTC().Add(-10 * time.Minute) // 10 minutes old

	staleTray2 := createTestTray("stale-tray-2", "other-type", trays.TrayStatusRegistered, 0)
	staleTray2.StatusChanged = time.Now().UTC().Add(-6 * time.Minute) // 6 minutes old

	// Fresh trays (newer than 5 minutes)
	freshTray1 := createTestTray("fresh-tray-1", "test-type", trays.TrayStatusRunning, 0)
	freshTray1.StatusChanged = time.Now().UTC().Add(-4 * time.Minute) // 4 minutes old

	freshTray2 := createTestTray("fresh-tray-2", "other-type", trays.TrayStatusDeleting, 0)
	freshTray2.StatusChanged = time.Now().UTC().Add(-1 * time.Minute) // 1 minute old

	// Insert all test trays
	insertTestTrays(t, collection, []*TestTray{staleTray1, staleTray2, freshTray1, freshTray2})

	// Test GetStale with 5 minute duration
	staleTrays, err := repo.GetStale(5 * time.Minute)
	if err != nil {
		t.Fatalf("GetStale failed: %v", err)
	}

	// Verify that only stale trays are returned
	if len(staleTrays) != 2 {
		t.Errorf("Expected 2 stale trays, got %d", len(staleTrays))
	}

	// Create a map of tray IDs for easier checking
	staleTraysMap := make(map[string]bool)
	for _, tray := range staleTrays {
		staleTraysMap[tray.Id] = true
	}

	// Check that the stale trays are in the result
	if !staleTraysMap["stale-tray-1"] {
		t.Error("Expected stale-tray-1 to be in the result")
	}

	if !staleTraysMap["stale-tray-2"] {
		t.Error("Expected stale-tray-2 to be in the result")
	}

	// Check that the fresh trays are not in the result
	if staleTraysMap["fresh-tray-1"] {
		t.Error("Expected fresh-tray-1 to not be in the result")
	}

	if staleTraysMap["fresh-tray-2"] {
		t.Error("Expected fresh-tray-2 to not be in the result")
	}

	// Test with no stale trays
	// Clear the collection
	err = collection.Drop(context.Background())
	if err != nil {
		t.Fatalf("Failed to drop collection: %v", err)
	}

	// Insert only fresh trays
	insertTestTrays(t, collection, []*TestTray{freshTray1, freshTray2})

	// Test GetStale again with 5 minute duration
	staleTrays, err = repo.GetStale(5 * time.Minute)
	if err != nil {
		t.Fatalf("GetStale failed: %v", err)
	}

	// Verify that no stale trays are returned
	if len(staleTrays) != 0 {
		t.Errorf("Expected 0 stale trays, got %d", len(staleTrays))
	}
}

// TestNewMongodbTrayRepository tests the NewMongodbTrayRepository constructor
func TestNewMongodbTrayRepository(t *testing.T) {
	// Create a new repository
	repo := NewMongodbTrayRepository()

	// Verify that the repository is not nil
	if repo == nil {
		t.Fatal("NewMongodbTrayRepository returned nil")
	}

	// Verify that the collection is nil (not connected yet)
	if repo.collection != nil {
		t.Errorf("Expected nil collection, got non-nil")
	}
}

// TestConnect tests the Connect method
func TestConnect(t *testing.T) {
	// Setup test collection
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create a new repository
	repo := NewMongodbTrayRepository()

	// Verify that the collection is nil before connecting
	if repo.collection != nil {
		t.Errorf("Expected nil collection before Connect, got non-nil")
	}

	// Connect to the collection
	repo.Connect(collection)

	// Verify that the collection is set correctly
	if repo.collection == nil {
		t.Fatal("Collection is nil after Connect")
	}

	// Verify that the collection is the same as the one we passed in
	if !reflect.DeepEqual(repo.collection, collection) {
		t.Errorf("Collection not set correctly")
	}

	// Test that we can use the repository after connecting
	// Insert a test tray
	testTray := createTestTray("test-connect", "test-type", trays.TrayStatusCreating, 0)
	insertTestTrays(t, collection, []*TestTray{testTray})

	// Try to get the tray using the repository
	tray, err := repo.GetById("test-connect")
	if err != nil {
		t.Fatalf("GetById failed after Connect: %v", err)
	}

	if tray == nil {
		t.Fatal("GetById returned nil tray after Connect")
	}

	if tray.Id != "test-connect" {
		t.Errorf("Expected tray ID 'test-connect', got '%s'", tray.Id)
	}
}

// TestCountByTrayType tests the CountByTrayType method
func TestCountByTrayType(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	// Create test repository
	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data with specific counts for each status
	// 2 Creating, 3 Registered, 1 Running, 2 Deleting for test-type
	testTrays := []*TestTray{
		createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0),
		createTestTray("test-tray-2", "test-type", trays.TrayStatusCreating, 0),
		createTestTray("test-tray-3", "test-type", trays.TrayStatusRegistered, 0),
		createTestTray("test-tray-4", "test-type", trays.TrayStatusRegistered, 0),
		createTestTray("test-tray-5", "test-type", trays.TrayStatusRegistered, 0),
		createTestTray("test-tray-6", "test-type", trays.TrayStatusRunning, 0),
		createTestTray("test-tray-7", "test-type", trays.TrayStatusDeleting, 0),
		createTestTray("test-tray-8", "test-type", trays.TrayStatusDeleting, 0),
		// Different tray type
		createTestTray("other-tray-1", "other-type", trays.TrayStatusCreating, 0),
		createTestTray("other-tray-2", "other-type", trays.TrayStatusRegistered, 0),
	}
	insertTestTrays(t, collection, testTrays)

	// Test CountByTrayType for test-type
	counts, total, err := repo.CountByTrayType("test-type")
	if err != nil {
		t.Fatalf("CountByTrayType failed: %v", err)
	}

	// Verify the total count
	expectedTotal := 8 // Total number of test-type trays
	if total != expectedTotal {
		t.Errorf("Expected total count %d, got %d", expectedTotal, total)
	}

	// Verify counts for each status
	expectedCounts := map[trays.TrayStatus]int{
		trays.TrayStatusCreating:    2,
		trays.TrayStatusRegistered:  3,
		trays.TrayStatusRunning:     1,
		trays.TrayStatusDeleting:    2,
		trays.TrayStatusRegistering: 0, // No trays with this status
	}

	for status, expectedCount := range expectedCounts {
		if counts[status] != expectedCount {
			t.Errorf("Expected count %d for status %v, got %d", expectedCount, status, counts[status])
		}
	}

	// Test CountByTrayType for other-type
	counts, total, err = repo.CountByTrayType("other-type")
	if err != nil {
		t.Fatalf("CountByTrayType for other-type failed: %v", err)
	}

	// Verify the total count for other-type
	expectedTotal = 2 // Total number of other-type trays
	if total != expectedTotal {
		t.Errorf("Expected total count %d for other-type, got %d", expectedTotal, total)
	}

	// Verify counts for each status for other-type
	expectedCounts = map[trays.TrayStatus]int{
		trays.TrayStatusCreating:    1,
		trays.TrayStatusRegistered:  1,
		trays.TrayStatusRunning:     0,
		trays.TrayStatusDeleting:    0,
		trays.TrayStatusRegistering: 0,
	}

	for status, expectedCount := range expectedCounts {
		if counts[status] != expectedCount {
			t.Errorf("Expected count %d for status %v in other-type, got %d", expectedCount, status, counts[status])
		}
	}

	// Test CountByTrayType with non-existent tray type
	counts, total, err = repo.CountByTrayType("non-existent")
	if err != nil {
		t.Fatalf("CountByTrayType with non-existent tray type failed: %v", err)
	}

	// Verify the total count for non-existent type
	if total != 0 {
		t.Errorf("Expected total count 0 for non-existent type, got %d", total)
	}

	// Verify that all status counts are 0 for non-existent type
	for status, count := range counts {
		if count != 0 {
			t.Errorf("Expected count 0 for status %v in non-existent type, got %d", status, count)
		}
	}
}
