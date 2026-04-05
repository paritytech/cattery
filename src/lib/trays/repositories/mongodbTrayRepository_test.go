//go:build integration

package repositories

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestTray is a helper struct to create test trays
type TestTray struct {
	Id            string           `bson:"id"`
	TrayTypeName  string           `bson:"trayTypeName"`
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
func createTestTray(id string, trayTypeName string, status trays.TrayStatus, jobRunId int64) *TestTray {
	return &TestTray{
		Id:            id,
		TrayTypeName:  trayTypeName,
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
	tray, err := repo.GetById(context.Background(),"test-tray-1")
	if err != nil {
		t.Fatalf("GetById failed: %v", err)
	}

	if tray == nil {
		t.Fatal("GetById returned nil tray")
	}

	if tray.Id != "test-tray-1" {
		t.Errorf("Expected tray ID 'test-tray-1', got '%s'", tray.Id)
	}

	if tray.TrayTypeName != "test-type" {
		t.Errorf("Expected tray type 'test-type', got '%s'", tray.TrayTypeName)
	}

	if tray.Status != trays.TrayStatusCreating {
		t.Errorf("Expected tray status %v, got %v", trays.TrayStatusCreating, tray.Status)
	}

	// Test GetById with non-existent ID
	tray, err = repo.GetById(context.Background(),"non-existent")
	if err != nil {
		t.Error("Expected no error for non-existent tray, got: ", err)
	}
	if tray != nil {
		t.Error("Expected tray to be nil for non-existent tray")
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
		Provider:      "docker",
		RunnerGroupId: 123,
		GitHubOrg:     "test-org",
		Config:        &config.DockerTrayConfig{Image: "alpine", NamePrefix: "test"},
	}

	tray, err := trays.NewTray(trayType)
	if err != nil {
		t.Fatalf("NewTray failed: %v", err)
	}
	// Set ProviderData and verify it round-trips
	tray.ProviderData["zone"] = "abc123"
	tray.ProviderData["something"] = "worker-1"

	// Test Save
	err = repo.Save(context.Background(),tray)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify the tray was saved
	savedTray, err := repo.GetById(context.Background(),tray.Id)
	if err != nil {
		t.Fatalf("Failed to get saved tray: %v", err)
	}

	if savedTray == nil {
		t.Fatal("GetById returned nil for saved tray")
	}

	if savedTray.Id != tray.Id {
		t.Errorf("Expected saved tray ID '%s', got '%s'", tray.Id, savedTray.Id)
	}

	if savedTray.TrayTypeName != tray.TrayTypeName {
		t.Errorf("Expected saved tray type '%s', got '%s'", tray.TrayTypeName, savedTray.TrayTypeName)
	}

	if savedTray.Status != tray.Status {
		t.Errorf("Expected saved tray status %v, got %v", tray.Status, savedTray.Status)
	}

	// Verify ProviderData was saved and loaded correctly
	if savedTray.ProviderData == nil {
		t.Fatalf("Expected ProviderData to be non-nil")
	}
	if savedTray.ProviderData["zone"] != "abc123" {
		t.Errorf("Expected ProviderData.zone to be 'abc123', got '%s'", savedTray.ProviderData["containerId"])
	}
	if savedTray.ProviderData["something"] != "worker-1" {
		t.Errorf("Expected ProviderData.something to be 'worker-1', got '%s'", savedTray.ProviderData["node"])
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
	updatedTray, err := repo.UpdateStatus(context.Background(),"test-tray-1", trays.TrayStatusRegistered, 123, 0, 0, "")
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
	updatedTray, err = repo.UpdateStatus(context.Background(),"test-tray-1", trays.TrayStatusRunning, 456, 333, 789, "")
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
	updatedTray, err = repo.UpdateStatus(context.Background(),"non-existent", trays.TrayStatusRegistered, 123, 0, 0, "")
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
	err := repo.Delete(context.Background(),"test-tray-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify the tray was deleted
	deletedTray, err := repo.GetById(context.Background(),"test-tray-1")
	if err != nil {
		t.Error("Expected no error for deleted tray, got: ", err)
	}

	if deletedTray != nil {
		t.Error("Expected nil for deleted tray, got non-nil")
	}

	// Test Delete with non-existent ID
	err = repo.Delete(context.Background(),"non-existent")
	if err != nil {
		t.Fatalf("Delete with non-existent ID failed: %v", err)
	}
}

// TestCountActive tests the CountActive method
func TestCountActive(t *testing.T) {
	client, collection := setupTestCollection(t)
	defer client.Disconnect(context.Background())

	repo := NewMongodbTrayRepository()
	repo.Connect(collection)

	// Insert test data: 2 Creating, 1 Registered, 1 Running, 2 Deleting for test-type
	testTrays := []*TestTray{
		createTestTray("test-tray-1", "test-type", trays.TrayStatusCreating, 0),
		createTestTray("test-tray-2", "test-type", trays.TrayStatusCreating, 0),
		createTestTray("test-tray-3", "test-type", trays.TrayStatusRegistered, 0),
		createTestTray("test-tray-4", "test-type", trays.TrayStatusRunning, 0),
		createTestTray("test-tray-5", "test-type", trays.TrayStatusDeleting, 0),
		createTestTray("test-tray-6", "test-type", trays.TrayStatusDeleting, 0),
		createTestTray("other-tray-1", "other-type", trays.TrayStatusCreating, 0),
	}
	insertTestTrays(t, collection, testTrays)

	// Active = all non-deleting = 2 + 1 + 1 = 4
	count, err := repo.CountActive(context.Background(), "test-type")
	if err != nil {
		t.Fatalf("CountActive failed: %v", err)
	}
	if count != 4 {
		t.Errorf("Expected 4 active trays, got %d", count)
	}

	// other-type: 1 active
	count, err = repo.CountActive(context.Background(), "other-type")
	if err != nil {
		t.Fatalf("CountActive for other-type failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 active tray for other-type, got %d", count)
	}

	// non-existent type: 0
	count, err = repo.CountActive(context.Background(), "non-existent")
	if err != nil {
		t.Fatalf("CountActive for non-existent type failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 active trays for non-existent type, got %d", count)
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
	staleTrays, err := repo.GetStale(context.Background(),5*time.Minute)
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
	staleTrays, err = repo.GetStale(context.Background(),5*time.Minute)
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
	tray, err := repo.GetById(context.Background(),"test-connect")
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

