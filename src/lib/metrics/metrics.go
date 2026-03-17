package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Counters

	staleTraysCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_stale_trays_count",
		Help: "Number of stale trays cleaned up",
	}, []string{"org", "tray_type"})

	preemptedTraysCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_preempted_trays_count",
		Help: "Number of preempted trays",
	}, []string{"org", "tray_type"})

	trayProviderErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_tray_provider_errors",
		Help: "Number of provider errors during tray operations",
	}, []string{"org", "provider", "tray_type", "operation_type"})

	scaleSetPollErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_scaleset_poll_errors",
		Help: "Number of scale set polling errors",
	}, []string{"org", "tray_type"})

	// Gauges

	registeredTraysTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_registered_trays",
		Help: "Number of currently registered trays",
	}, []string{"org", "tray_type"})

	scaleSetPendingJobs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_pending_jobs",
		Help: "Number of pending jobs reported by scale set statistics",
	}, []string{"org", "tray_type"})
)

// StaleTrays

func StaleTraysAdd(org string, trayType string, count int) {
	staleTraysCount.WithLabelValues(org, trayType).Add(float64(count))
}

func StaleTraysInc(org string, trayType string) {
	StaleTraysAdd(org, trayType, 1)
}

// PreemptedTrays

func PreemptedTraysAdd(org string, trayType string, count int) {
	preemptedTraysCount.WithLabelValues(org, trayType).Add(float64(count))
}

func PreemptedTraysInc(org string, trayType string) {
	PreemptedTraysAdd(org, trayType, 1)
}

// RegisteredTrays

func RegisteredTraysAdd(org string, trayType string, count int) {
	registeredTraysTotal.WithLabelValues(org, trayType).Add(float64(count))
}

// TrayProviderErrors

func TrayProviderErrors(org string, provider, trayType string, operationType string) {
	trayProviderErrors.WithLabelValues(org, provider, trayType, operationType).Inc()
}

// ScaleSet metrics

func ScaleSetPollErrorsInc(org string, trayType string) {
	scaleSetPollErrors.WithLabelValues(org, trayType).Inc()
}

func ScaleSetPendingJobsSet(org string, trayType string, count int) {
	scaleSetPendingJobs.WithLabelValues(org, trayType).Set(float64(count))
}
