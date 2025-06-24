package jobQueue

import (
	"cattery/lib/jobs"
	"sync"
	"testing"
)

func TestNewJobQueue(t *testing.T) {
	queue := NewJobQueue()

	if queue == nil {
		t.Error("Expected non-nil JobQueue")
	}

	if queue.jobs == nil {
		t.Error("Expected non-nil jobs map")
	}

	if queue.groups == nil {
		t.Error("Expected non-nil groups map")
	}

	if queue.rwMutex == nil {
		t.Error("Expected non-nil rwMutex")
	}

	if len(queue.jobs) != 0 {
		t.Errorf("Expected empty jobs map, got %d items", len(queue.jobs))
	}

	if len(queue.groups) != 0 {
		t.Errorf("Expected empty groups map, got %d items", len(queue.groups))
	}
}

func TestAdd(t *testing.T) {
	queue := NewJobQueue()
	job := &jobs.Job{
		Id:       1,
		Name:     "Test Job",
		TrayType: "TestTray",
	}

	// Test adding a job
	queue.Add(job)

	if len(queue.jobs) != 1 {
		t.Errorf("Expected 1 job, got %d", len(queue.jobs))
	}

	if len(queue.groups) != 1 {
		t.Errorf("Expected 1 group, got %d", len(queue.groups))
	}

	if len(queue.groups["TestTray"]) != 1 {
		t.Errorf("Expected 1 job in TestTray group, got %d", len(queue.groups["TestTray"]))
	}

	// Test adding a duplicate job (should be ignored)
	queue.Add(job)

	if len(queue.jobs) != 1 {
		t.Errorf("Expected still 1 job after duplicate add, got %d", len(queue.jobs))
	}

	// Test adding a different job with the same tray type
	job2 := &jobs.Job{
		Id:       2,
		Name:     "Test Job 2",
		TrayType: "TestTray",
	}

	queue.Add(job2)

	if len(queue.jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(queue.jobs))
	}

	if len(queue.groups["TestTray"]) != 2 {
		t.Errorf("Expected 2 jobs in TestTray group, got %d", len(queue.groups["TestTray"]))
	}

	// Test adding a job with a different tray type
	job3 := &jobs.Job{
		Id:       3,
		Name:     "Test Job 3",
		TrayType: "AnotherTray",
	}

	queue.Add(job3)

	if len(queue.jobs) != 3 {
		t.Errorf("Expected 3 jobs, got %d", len(queue.jobs))
	}

	if len(queue.groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(queue.groups))
	}

	if len(queue.groups["AnotherTray"]) != 1 {
		t.Errorf("Expected 1 job in AnotherTray group, got %d", len(queue.groups["AnotherTray"]))
	}
}

func TestGet(t *testing.T) {
	queue := NewJobQueue()
	job := &jobs.Job{
		Id:       1,
		Name:     "Test Job",
		TrayType: "TestTray",
	}

	queue.Add(job)

	// Test getting an existing job
	retrievedJob := queue.Get(1)

	if retrievedJob == nil {
		t.Error("Expected non-nil job")
		return
	}

	if retrievedJob.Id != 1 {
		t.Errorf("Expected job ID 1, got %d", retrievedJob.Id)
	}

	if retrievedJob.Name != "Test Job" {
		t.Errorf("Expected job name 'Test Job', got '%s'", retrievedJob.Name)
	}

	if retrievedJob.TrayType != "TestTray" {
		t.Errorf("Expected tray type 'TestTray', got '%s'", retrievedJob.TrayType)
	}

	// Test getting a non-existent job
	nonExistentJob := queue.Get(999)

	if nonExistentJob != nil {
		t.Error("Expected nil for non-existent job")
	}
}

func TestGetGroup(t *testing.T) {
	queue := NewJobQueue()
	job1 := &jobs.Job{
		Id:       1,
		Name:     "Test Job 1",
		TrayType: "TestTray",
	}

	job2 := &jobs.Job{
		Id:       2,
		Name:     "Test Job 2",
		TrayType: "TestTray",
	}

	queue.Add(job1)
	queue.Add(job2)

	// Test getting an existing group
	group := queue.GetGroup("TestTray")

	if len(group) != 2 {
		t.Errorf("Expected 2 jobs in group, got %d", len(group))
	}

	if _, exists := group[1]; !exists {
		t.Error("Expected job with ID 1 in group")
	}

	if _, exists := group[2]; !exists {
		t.Error("Expected job with ID 2 in group")
	}

	// Test getting a non-existent group (should create an empty group)
	nonExistentGroup := queue.GetGroup("NonExistentTray")

	if nonExistentGroup == nil {
		t.Error("Expected non-nil group for non-existent tray type")
	}

	if len(nonExistentGroup) != 0 {
		t.Errorf("Expected empty group for non-existent tray type, got %d items", len(nonExistentGroup))
	}

	// Verify the new group was created
	if len(queue.groups) != 2 {
		t.Errorf("Expected 2 groups after getting non-existent group, got %d", len(queue.groups))
	}
}

func TestGetJobsCount(t *testing.T) {
	queue := NewJobQueue()

	// Test with empty queue
	counts := queue.GetJobsCount()

	if len(counts) != 0 {
		t.Errorf("Expected empty counts map for empty queue, got %d items", len(counts))
	}

	// Add some jobs
	job1 := &jobs.Job{
		Id:       1,
		Name:     "Test Job 1",
		TrayType: "TestTray1",
	}

	job2 := &jobs.Job{
		Id:       2,
		Name:     "Test Job 2",
		TrayType: "TestTray1",
	}

	job3 := &jobs.Job{
		Id:       3,
		Name:     "Test Job 3",
		TrayType: "TestTray2",
	}

	queue.Add(job1)
	queue.Add(job2)
	queue.Add(job3)

	// Test with populated queue
	counts = queue.GetJobsCount()

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

func TestDelete(t *testing.T) {
	queue := NewJobQueue()
	job1 := &jobs.Job{
		Id:       1,
		Name:     "Test Job 1",
		TrayType: "TestTray",
	}

	job2 := &jobs.Job{
		Id:       2,
		Name:     "Test Job 2",
		TrayType: "TestTray",
	}

	queue.Add(job1)
	queue.Add(job2)

	// Verify initial state
	if len(queue.jobs) != 2 {
		t.Errorf("Expected 2 jobs initially, got %d", len(queue.jobs))
	}

	if len(queue.groups["TestTray"]) != 2 {
		t.Errorf("Expected 2 jobs in TestTray group initially, got %d", len(queue.groups["TestTray"]))
	}

	// Test deleting an existing job
	queue.Delete(1)

	if len(queue.jobs) != 1 {
		t.Errorf("Expected 1 job after deletion, got %d", len(queue.jobs))
	}

	if len(queue.groups["TestTray"]) != 1 {
		t.Errorf("Expected 1 job in TestTray group after deletion, got %d", len(queue.groups["TestTray"]))
	}

	if _, exists := queue.jobs[1]; exists {
		t.Error("Expected job with ID 1 to be deleted from jobs map")
	}

	if _, exists := queue.groups["TestTray"][1]; exists {
		t.Error("Expected job with ID 1 to be deleted from TestTray group")
	}

	// Test deleting a non-existent job (should not cause errors)
	queue.Delete(999)

	if len(queue.jobs) != 1 {
		t.Errorf("Expected still 1 job after non-existent deletion, got %d", len(queue.jobs))
	}

	// Delete the last job
	queue.Delete(2)

	if len(queue.jobs) != 0 {
		t.Errorf("Expected 0 jobs after deleting all jobs, got %d", len(queue.jobs))
	}

	if len(queue.groups["TestTray"]) != 0 {
		t.Errorf("Expected 0 jobs in TestTray group after deleting all jobs, got %d", len(queue.groups["TestTray"]))
	}
}

func TestConcurrentOperations(t *testing.T) {
	queue := NewJobQueue()

	// Number of concurrent operations
	const numOperations = 100

	// WaitGroup to wait for all goroutines to finish
	var wg sync.WaitGroup
	wg.Add(numOperations * 3) // Add, Get, Delete operations

	// Test concurrent Add operations
	for i := 0; i < numOperations; i++ {
		go func(id int64) {
			defer wg.Done()
			job := &jobs.Job{
				Id:       id,
				Name:     "Concurrent Job",
				TrayType: "ConcurrentTray",
			}
			queue.Add(job)
		}(int64(i + 1))
	}

	// Test concurrent Get operations
	for i := 0; i < numOperations; i++ {
		go func(id int64) {
			defer wg.Done()
			// Get may return nil if the job hasn't been added yet, which is fine
			_ = queue.Get(id)
		}(int64(i + 1))
	}

	// Test concurrent Delete operations
	for i := 0; i < numOperations; i++ {
		go func(id int64) {
			defer wg.Done()
			queue.Delete(id)
		}(int64(i + 1))
	}

	// Wait for all goroutines to finish
	wg.Wait()

	// Verify final state
	// Since we're adding and deleting the same jobs concurrently,
	// we can't predict exactly how many will be in the queue at the end.
	// But we can verify that the queue is in a consistent state.

	// Get the count of jobs in each group
	counts := queue.GetJobsCount()

	// Verify that the count in the ConcurrentTray group matches the actual number of jobs
	if counts["ConcurrentTray"] != len(queue.GetGroup("ConcurrentTray")) {
		t.Errorf("Inconsistent state: count %d doesn't match actual group size %d",
			counts["ConcurrentTray"], len(queue.GetGroup("ConcurrentTray")))
	}

	// Verify that the total number of jobs matches the sum of jobs in all groups
	totalJobsInGroups := 0
	for _, count := range counts {
		totalJobsInGroups += count
	}

	if len(queue.jobs) != totalJobsInGroups {
		t.Errorf("Inconsistent state: total jobs %d doesn't match sum of jobs in groups %d",
			len(queue.jobs), totalJobsInGroups)
	}
}
