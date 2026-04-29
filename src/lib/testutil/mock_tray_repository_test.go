package testutil

import (
	"cattery/lib/trays"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mock is used heavily by trayManager and handler tests, and the
// load-bearing CreateTray flow depends on its merge semantics matching the
// Mongo impl. Lock those down here so a regression in the mock can't quietly
// invalidate downstream test assertions.

func TestMock_SetProviderData_AddsKeysToEmptyMap(t *testing.T) {
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{Id: "t1", ProviderData: map[string]string{}}

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{
		"zone":       "us-east-1",
		"instanceId": "i-12345",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "us-east-1", updated.ProviderData["zone"])
	assert.Equal(t, "i-12345", updated.ProviderData["instanceId"])
}

func TestMock_SetProviderData_MergesWithoutClobbering(t *testing.T) {
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{Id: "t1", ProviderData: map[string]string{
		"zone":       "us-east-1",
		"instanceId": "i-original",
	}}

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{
		"instanceId": "i-overwritten",
		"region":     "us-east",
	})
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", updated.ProviderData["zone"], "untouched key must survive")
	assert.Equal(t, "i-overwritten", updated.ProviderData["instanceId"])
	assert.Equal(t, "us-east", updated.ProviderData["region"])
}

func TestMock_SetProviderData_DoesNotChangeStatus(t *testing.T) {
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{
		Id:           "t1",
		Status:       trays.TrayStatusDeleting,
		ProviderData: map[string]string{},
	}

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{"zone": "z"})
	require.NoError(t, err)
	assert.Equal(t, trays.TrayStatusDeleting, updated.Status,
		"SetProviderData must not revert a status change made by another goroutine")
}

func TestMock_SetProviderData_DoesNotBumpStatusChanged(t *testing.T) {
	original := time.Now().Add(-10 * time.Minute)
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{
		Id:            "t1",
		Status:        trays.TrayStatusCreating,
		StatusChanged: original,
		ProviderData:  map[string]string{},
	}

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{"zone": "z"})
	require.NoError(t, err)
	assert.WithinDuration(t, original, updated.StatusChanged, time.Second,
		"statusChanged must be preserved so the stale handler's clock keeps ticking")
}

func TestMock_SetProviderData_ReturnsNilForMissingTray(t *testing.T) {
	repo := NewMockTrayRepository()

	updated, err := repo.SetProviderData(context.Background(), "missing", map[string]string{"zone": "z"})
	require.NoError(t, err)
	assert.Nil(t, updated)
}

func TestMock_SetProviderData_HandlesNilProviderData(t *testing.T) {
	// Realistic case: a tray inserted directly into the mock without
	// initializing ProviderData. The mock must treat nil as empty rather
	// than panicking on map write.
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{Id: "t1", ProviderData: nil}

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{"zone": "z"})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "z", updated.ProviderData["zone"])
}

func TestMock_SetProviderData_RespectsSetErr(t *testing.T) {
	repo := NewMockTrayRepository()
	repo.Trays["t1"] = &trays.Tray{Id: "t1", ProviderData: map[string]string{}}
	repo.SetErr = errors.New("simulated db error")

	updated, err := repo.SetProviderData(context.Background(), "t1", map[string]string{"zone": "z"})
	require.Error(t, err)
	assert.Nil(t, updated)
}
