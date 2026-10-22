package config_test

import (
	"cattery/lib/config"
	"cattery/lib/scaleSetClient"
	"testing"
)

// The poll retry budget and the listener-liveness grace period live in
// different packages and are easy to tune apart. If one GetMessage can take
// longer than the grace period, a single slow call makes a healthy listener
// look dead and the stale reaper starts sparing trays it should remove.
func TestPollerGraceExceedsMaxPollDuration(t *testing.T) {
	grace := config.StaleConfig{}.WithDefaults().PollerGrace
	if grace <= scaleSetClient.MaxPollDuration {
		t.Fatalf("PollerGrace %s must exceed worst-case poll %s",
			grace, scaleSetClient.MaxPollDuration)
	}
}
