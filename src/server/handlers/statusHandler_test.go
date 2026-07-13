package handlers

import (
	"bytes"
	"os"
	"testing"
	"time"

	"cattery/lib/config"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trays"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusTemplateRenders executes the status template with representative
// data so template syntax or func errors fail in CI, not on first page load.
// Set STATUS_RENDER_OUT to a file path to dump the rendered HTML for a visual check.
func TestStatusTemplateRenders(t *testing.T) {
	now := time.Now().UTC()
	data := struct {
		Now       time.Time
		Version   string
		Trays     []*trays.Tray
		Messages  []*scaleSetPoller.Message
		Orgs      []*config.GitHubOrganization
		Providers []*config.ProviderConfig
		TrayTypes []*config.TrayType
	}{
		Now:     now,
		Version: "v0.0.0-test",
		Trays: []*trays.Tray{
			{
				Id:            "tray-1",
				TrayTypeName:  "gce-large",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusRunning,
				StatusChanged: now.Add(-3 * time.Minute),
				Repository:    "test-org/repo",
				WorkflowName:  "CI",
				JobName:       "build-and-test-with-a-very-long-job-name-that-overflows",
				WorkflowRunId: 42,
				JobRunId:      7,
			},
			{
				Id:            "tray-2",
				TrayTypeName:  "gce-large",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusRegistered,
				StatusChanged: now.Add(-30 * time.Second),
			},
			{
				Id:            "tray-3",
				TrayTypeName:  "docker-small",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusCreating,
				StatusChanged: now.Add(-5 * time.Second),
			},
			{
				Id:            "tray-4",
				TrayTypeName:  "nomad-spot",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusRunning,
				StatusChanged: now.Add(-10 * time.Minute),
			},
			{
				Id:            "tray-5",
				TrayTypeName:  "nomad-spot",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusRunning,
				StatusChanged: now.Add(-8 * time.Minute),
			},
			{
				Id:            "tray-6",
				TrayTypeName:  "docker-small",
				GitHubOrgName: "test-org",
				Status:        trays.TrayStatusDeleting,
				StatusChanged: now.Add(-time.Minute),
			},
		},
		Messages: []*scaleSetPoller.Message{
			{
				Time:         now.Add(-time.Minute),
				Kind:         scaleSetPoller.MessageKindScale,
				TrayType:     "gce-large",
				DesiredCount: 2,
				Stats:        &scaleSetPoller.ScaleStats{Available: 1, Assigned: 1, Running: 1, Busy: 1, Idle: 1, Registered: 2},
			},
			{
				Time:           now.Add(-2 * time.Minute),
				Kind:           scaleSetPoller.MessageKindJobStarted,
				TrayType:       "gce-large",
				Repository:     "test-org/repo",
				WorkflowRunID:  42,
				JobID:          7,
				JobDisplayName: "build",
				RunnerName:     "tray-1",
			},
		},
		Orgs:      []*config.GitHubOrganization{{Name: "test-org"}},
		Providers: []*config.ProviderConfig{{"name": "gce", "type": "gce"}},
		TrayTypes: []*config.TrayType{
			{Name: "gce-large", Provider: "gce", GitHubOrg: "test-org", RunnerGroupId: 1, MaxTrays: 5},
			{Name: "docker-small", Provider: "docker", GitHubOrg: "test-org", RunnerGroupId: 1},
			{Name: "nomad-spot", Provider: "nomad", GitHubOrg: "test-org", RunnerGroupId: 1, MaxTrays: 2},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, statusTmpl.Execute(&buf, data))
	out := buf.String()

	// Tray type limits reach the client via data attributes on the tray types table.
	assert.Contains(t, out, `data-max="5"`)
	assert.Contains(t, out, "gce-large")
	assert.Contains(t, out, `data-id="tray-1"`)
	assert.Contains(t, out, "https://github.com/test-org/repo/actions/runs/42/job/7")

	if path := os.Getenv("STATUS_RENDER_OUT"); path != "" {
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	}
}
