package metrics

import (
	"cattery/lib/trays"
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

type stubLister struct {
	trays []*trays.Tray
	err   error
}

func (s *stubLister) ListTrays(_ context.Context) ([]*trays.Tray, error) {
	return s.trays, s.err
}

func TestJobFinished_RecordsSecondsAndCount(t *testing.T) {
	JobFinished("test-org", "test-type", "test-repo", 12.5)

	assert.Equal(t, 12.5, testutil.ToFloat64(
		jobSecondsTotal.WithLabelValues("test-org", "test-type", "test-repo")))
	assert.Equal(t, 1.0, testutil.ToFloat64(
		jobsTotal.WithLabelValues("test-org", "test-type", "test-repo")))
}

func TestJobFinished_AccumulatesPerRepository(t *testing.T) {
	JobFinished("acc-org", "acc-type", "repo-a", 10)
	JobFinished("acc-org", "acc-type", "repo-a", 5)
	JobFinished("acc-org", "acc-type", "repo-b", 3)

	assert.Equal(t, 15.0, testutil.ToFloat64(
		jobSecondsTotal.WithLabelValues("acc-org", "acc-type", "repo-a")))
	assert.Equal(t, 2.0, testutil.ToFloat64(
		jobsTotal.WithLabelValues("acc-org", "acc-type", "repo-a")))
	assert.Equal(t, 3.0, testutil.ToFloat64(
		jobSecondsTotal.WithLabelValues("acc-org", "acc-type", "repo-b")))
}

func TestTrayCollector_GroupsByRepository(t *testing.T) {
	c := &trayCollector{lister: &stubLister{trays: []*trays.Tray{
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "repo-a", Status: trays.TrayStatusRunning},
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "repo-a", Status: trays.TrayStatusRunning},
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "repo-b", Status: trays.TrayStatusRunning},
	}}}

	expected := `
# HELP cattery_registered_trays Number of currently registered trays
# TYPE cattery_registered_trays gauge
cattery_registered_trays{org="org",repository="repo-a",tray_type="big"} 2
cattery_registered_trays{org="org",repository="repo-b",tray_type="big"} 1
`
	assert.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected)))
}

func TestTrayCollector_UnassignedTrayReportsEmptyRepository(t *testing.T) {
	// A tray that has not been given a job yet has no repository. It still
	// occupies a runner, so it must be reported rather than dropped.
	c := &trayCollector{lister: &stubLister{trays: []*trays.Tray{
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "", Status: trays.TrayStatusRegistered},
	}}}

	expected := `
# HELP cattery_registered_trays Number of currently registered trays
# TYPE cattery_registered_trays gauge
cattery_registered_trays{org="org",repository="",tray_type="big"} 1
`
	assert.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected)))
}

func TestTrayCollector_ExcludesDeletingTrays(t *testing.T) {
	c := &trayCollector{lister: &stubLister{trays: []*trays.Tray{
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "repo-a", Status: trays.TrayStatusRunning},
		{GitHubOrgName: "org", TrayTypeName: "big", Repository: "repo-a", Status: trays.TrayStatusDeleting},
	}}}

	expected := `
# HELP cattery_registered_trays Number of currently registered trays
# TYPE cattery_registered_trays gauge
cattery_registered_trays{org="org",repository="repo-a",tray_type="big"} 1
`
	assert.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected)))
}
