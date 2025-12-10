package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Counters

	staleTraysCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_stale_trays_count",
		Help: "",
	}, []string{"org", "tray_type"})

	staleJobsCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_stale_jobs_count",
		Help: "",
	}, []string{"org", "repository", "job_name", "tray_type"})

	preemptedTraysCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cattery_preempted_trays_count",
		Help: "",
	}, []string{"org", "tray_type"})

	// Gauges

	registeredTraysTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_trays_total",
		Help: "",
	}, []string{"org", "tray_type"})

	jobsInQueueTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_jobs_in_queue_total",
		Help: "",
	}, []string{"org", "repository", "job_name", "tray_type"})
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

// StaleJobs

func StaleJobsAdd(org string, repository string, jobName string, trayType string, count int) {
	staleJobsCount.WithLabelValues(org, repository, jobName, trayType).Add(float64(count))
}

func StaleJobsInc(org string, repository string, jobName string, trayType string) {
	StaleJobsAdd(org, repository, jobName, trayType, 1)
}

// registeredTraysTotal

func RegisteredTraysAdd(org string, trayType string, count int) {
	registeredTraysTotal.WithLabelValues(org, trayType).Add(float64(count))
}

// jobsInQueueTotal

func JobsInQueueAdd(org string, repository string, jobName string, trayType string, count int) {
	jobsInQueueTotal.WithLabelValues(org, repository, jobName, trayType).Add(float64(count))
}
