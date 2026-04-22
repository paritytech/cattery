package metrics

import (
	"cattery/lib/trays"
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
)

// TrayLister is the subset of TrayManager needed by the metrics collector.
type TrayLister interface {
	ListTrays(ctx context.Context) ([]*trays.Tray, error)
}

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

	scaleSetPendingJobs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_pending_jobs",
		Help: "Number of available (queued) jobs reported by scale set statistics",
	}, []string{"org", "tray_type"})

	scaleSetAssignedJobs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_assigned_jobs",
		Help: "Number of jobs assigned to runners reported by scale set statistics",
	}, []string{"org", "tray_type"})

	scaleSetRunningJobs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_running_jobs",
		Help: "Number of currently running jobs reported by scale set statistics",
	}, []string{"org", "tray_type"})

	scaleSetBusyRunners = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_busy_runners",
		Help: "Number of busy runners reported by scale set statistics",
	}, []string{"org", "tray_type"})

	scaleSetIdleRunners = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_idle_runners",
		Help: "Number of idle runners reported by scale set statistics",
	}, []string{"org", "tray_type"})

	scaleSetRegisteredRunners = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cattery_scaleset_registered_runners",
		Help: "Number of registered runners reported by scale set statistics",
	}, []string{"org", "tray_type"})

	registeredTraysDesc = prometheus.NewDesc(
		"cattery_registered_trays",
		"Number of currently registered trays",
		[]string{"org", "tray_type"}, nil,
	)
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

func ScaleSetAssignedJobsSet(org string, trayType string, count int) {
	scaleSetAssignedJobs.WithLabelValues(org, trayType).Set(float64(count))
}

func ScaleSetRunningJobsSet(org string, trayType string, count int) {
	scaleSetRunningJobs.WithLabelValues(org, trayType).Set(float64(count))
}

func ScaleSetBusyRunnersSet(org string, trayType string, count int) {
	scaleSetBusyRunners.WithLabelValues(org, trayType).Set(float64(count))
}

func ScaleSetIdleRunnersSet(org string, trayType string, count int) {
	scaleSetIdleRunners.WithLabelValues(org, trayType).Set(float64(count))
}

func ScaleSetRegisteredRunnersSet(org string, trayType string, count int) {
	scaleSetRegisteredRunners.WithLabelValues(org, trayType).Set(float64(count))
}

// trayCollector queries the database on each Prometheus scrape.
type trayCollector struct {
	lister TrayLister
}

func (c *trayCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- registeredTraysDesc
}

func (c *trayCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	allTrays, err := c.lister.ListTrays(ctx)
	if err != nil {
		log.Errorf("metrics: failed to list trays: %v", err)
		return
	}

	counts := make(map[[2]string]int)
	for _, t := range allTrays {
		if t.Status != trays.TrayStatusDeleting {
			counts[[2]string{t.GitHubOrgName, t.TrayTypeName}]++
		}
	}

	for key, count := range counts {
		ch <- prometheus.MustNewConstMetric(registeredTraysDesc, prometheus.GaugeValue, float64(count), key[0], key[1])
	}
}

// RegisterTrayCollector registers the DB-backed registered trays collector.
func RegisterTrayCollector(lister TrayLister) {
	prometheus.MustRegister(&trayCollector{lister: lister})
}
