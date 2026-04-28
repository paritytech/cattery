package trays

import (
	"cattery/lib/config"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTrayStatusString(t *testing.T) {
	tests := []struct {
		status   TrayStatus
		expected string
	}{
		{TrayStatusCreating, "creating"},
		{TrayStatusRegistering, "registering"},
		{TrayStatusRegistered, "registered"},
		{TrayStatusRunning, "running"},
		{TrayStatusDeleting, "deleting"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestTrayStatusFromString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TrayStatus
		wantErr bool
	}{
		{"creating", "creating", TrayStatusCreating, false},
		{"registering", "registering", TrayStatusRegistering, false},
		{"registered", "registered", TrayStatusRegistered, false},
		{"running", "running", TrayStatusRunning, false},
		{"deleting", "deleting", TrayStatusDeleting, false},
		{"uppercase", "CREATING", TrayStatusCreating, false},
		{"mixed case", "Registered", TrayStatusRegistered, false},
		{"unknown", "bogus", 0, true},
		{"empty", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TrayStatusFromString(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestTrayStatusUnknownValue(t *testing.T) {
	unknown := TrayStatus(99)
	assert.Equal(t, "", unknown.String())
}

func TestNewTray(t *testing.T) {
	trayType := config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	tray, err := NewTray(trayType)

	assert.NoError(t, err)
	assert.NotNil(t, tray)
	assert.True(t, strings.HasPrefix(tray.Id, "test-type-"))
	assert.Equal(t, "test-type", tray.TrayTypeName)
	assert.Equal(t, "docker", tray.ProviderName)
	assert.Equal(t, "test-org", tray.GitHubOrgName)
	assert.Equal(t, TrayStatusCreating, tray.Status)
	assert.NotNil(t, tray.ProviderData)
}

func TestNewTrayIdIsUnique(t *testing.T) {
	trayType := config.TrayType{
		Name:      "test-type",
		Provider:  "docker",
		GitHubOrg: "test-org",
	}

	tray1, _ := NewTray(trayType)
	tray2, _ := NewTray(trayType)

	assert.NotEqual(t, tray1.Id, tray2.Id)
}

func TestTrayString(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tray := &Tray{
		Id:            "test-type-abc123",
		TrayTypeName:  "test-type",
		Status:        TrayStatusRunning,
		GitHubOrgName: "test-org",
		StatusChanged: now,
	}

	result := tray.String()

	assert.Contains(t, result, "test-type-abc123")
	assert.Contains(t, result, "test-type")
	assert.Contains(t, result, "running")
	assert.Contains(t, result, "test-org")
	assert.Contains(t, result, "2025-01-15T10:30:00Z")
}
