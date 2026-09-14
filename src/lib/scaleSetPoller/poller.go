package scaleSetPoller

import (
	"cattery/lib/metrics"
	"cattery/lib/scaleSetClient"
	"cattery/lib/trayManager"
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"cattery/lib/config"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	log "github.com/sirupsen/logrus"
)

type Poller struct {
	client      *scaleSetClient.ScaleSetClient
	trayType    *config.TrayType
	trayManager *trayManager.TrayManager
	history     *History
	logger      *log.Entry
}

func NewPoller(
	client *scaleSetClient.ScaleSetClient,
	trayType *config.TrayType,
	tm *trayManager.TrayManager,
) *Poller {
	return &Poller{
		client:      client,
		trayType:    trayType,
		trayManager: tm,
		history:     &History{},
		logger: log.WithFields(log.Fields{
			"component": "scaleSetPoller",
			"trayType":  trayType.Name,
		}),
	}
}

func (p *Poller) History() *History {
	return p.history
}

func (p *Poller) Run(ctx context.Context) error {
	p.logger.Info("Starting scale set poller")

	if err := p.client.EnsureScaleSet(ctx); err != nil {
		return fmt.Errorf("failed to ensure scale set: %w", err)
	}

	if err := p.client.CreateSession(ctx); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := p.client.Close(closeCtx); err != nil {
			p.logger.Errorf("Failed to close session: %v", err)
		}
	}()

	scaleSetID := p.client.GetScaleSetID()

	scaler := &catteryScaler{poller: p}

	l, err := listener.New(
		&sessionAdapter{client: p.client},
		listener.Config{
			ScaleSetID: scaleSetID,
			MaxRunners: p.trayType.MaxTrays,
		},
		listener.WithMetricsRecorder(scaler),
	)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	p.logger.Info("Entering listener loop")
	return l.Run(ctx, scaler)
}

// sessionAdapter adapts our ScaleSetClient to the listener.Client interface.
type sessionAdapter struct {
	client *scaleSetClient.ScaleSetClient
}

func (s *sessionAdapter) GetMessage(ctx context.Context, lastMessageID, maxCapacity int) (*scaleset.RunnerScaleSetMessage, error) {
	return s.client.Poll(ctx, lastMessageID, maxCapacity)
}

func (s *sessionAdapter) DeleteMessage(ctx context.Context, messageID int) error {
	return s.client.Ack(ctx, messageID)
}

func (s *sessionAdapter) AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error) {
	return s.client.AcquireJobs(ctx, requestIDs)
}

func (s *sessionAdapter) Session() scaleset.RunnerScaleSetSession {
	return s.client.Session()
}

// catteryScaler implements the listener.Scaler and listener.MetricsRecorder interfaces.
type catteryScaler struct {
	poller     *Poller
	latestStat atomic.Pointer[scaleset.RunnerScaleSetStatistic]
}

// MetricsRecorder implementation.

func (cs *catteryScaler) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	cs.latestStat.Store(statistics)

	org := cs.poller.trayType.GitHubOrg
	name := cs.poller.trayType.Name
	metrics.ScaleSetPendingJobsSet(org, name, statistics.TotalAvailableJobs)
	metrics.ScaleSetAssignedJobsSet(org, name, statistics.TotalAssignedJobs)
	metrics.ScaleSetRunningJobsSet(org, name, statistics.TotalRunningJobs)
	metrics.ScaleSetBusyRunnersSet(org, name, statistics.TotalBusyRunners)
	metrics.ScaleSetIdleRunnersSet(org, name, statistics.TotalIdleRunners)
	metrics.ScaleSetRegisteredRunnersSet(org, name, statistics.TotalRegisteredRunners)
}
func (cs *catteryScaler) RecordJobStarted(msg *scaleset.JobStarted)     {}
func (cs *catteryScaler) RecordJobCompleted(msg *scaleset.JobCompleted) {}
func (cs *catteryScaler) RecordDesiredRunners(count int)                {}

func (cs *catteryScaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	cs.recordScaleMessage(count)

	// Scaling failures (provider or DB) are transient from the session's point
	// of view: the next statistics message triggers another attempt. Failing
	// the session would only add a 30s+ gap during which job events are lost.
	if err := cs.poller.trayManager.ScaleForDemand(ctx, cs.poller.trayType, count); err != nil {
		cs.recordHandlerError(fmt.Sprintf("scale for demand (%d)", count), err)
	}

	active, err := cs.poller.trayManager.CountTrays(ctx, cs.poller.trayType.Name)
	if err != nil {
		cs.recordHandlerError("count trays", err)
		return 0, nil
	}
	return active, nil
}

// recordHandlerError logs a failure inside a listener callback and counts it
// in cattery_scaleset_poll_errors. The scaleset listener acks a message before
// invoking the callbacks and aborts the whole session on any callback error,
// so per-tray failures must never propagate back to it: the acked batch would
// be lost, and every JobStarted arriving before the session is recreated would
// leave its tray without a workflow run id (and so unrestartable on preemption).
func (cs *catteryScaler) recordHandlerError(what string, err error) {
	cs.poller.logger.Errorf("Failed to %s: %v", what, err)
	metrics.ScaleSetPollErrorsInc(cs.poller.trayType.GitHubOrg, cs.poller.trayType.Name)
}

func (cs *catteryScaler) recordScaleMessage(count int) {
	msg := &Message{
		Time:         time.Now(),
		Kind:         MessageKindScale,
		TrayType:     cs.poller.trayType.Name,
		DesiredCount: count,
	}
	if stats := cs.latestStat.Load(); stats != nil {
		msg.Stats = &ScaleStats{
			Available:  stats.TotalAvailableJobs,
			Assigned:   stats.TotalAssignedJobs,
			Running:    stats.TotalRunningJobs,
			Busy:       stats.TotalBusyRunners,
			Idle:       stats.TotalIdleRunners,
			Registered: stats.TotalRegisteredRunners,
		}
	}
	cs.poller.history.Add(msg)
}

func (cs *catteryScaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	cs.poller.logger.Infof("Job started: %s on runner %s (workflow run %d)",
		jobInfo.JobDisplayName, jobInfo.RunnerName, jobInfo.WorkflowRunID)

	jobID, _ := strconv.ParseInt(jobInfo.JobID, 10, 64)
	workflowName := parseWorkflowName(jobInfo.JobWorkflowRef)

	// The tray stores the bare repository name: the restarter passes it to
	// GitHub API calls that take the owner separately. Storing the full name
	// here broke the restarter (duplicated owner in the API URL).
	//
	// A failure here is per-tray, not session-level, so it is logged and
	// counted but not returned: the listener has already acked the message,
	// and a returned error would make it drop the session and every job
	// event that arrives before the session is recreated (see recordHandlerError).
	tray, err := cs.poller.trayManager.SetJob(ctx, jobInfo.RunnerName, jobID, jobInfo.WorkflowRunID, jobInfo.RepositoryName, jobInfo.JobDisplayName, workflowName)
	if err != nil {
		cs.recordHandlerError("set job on tray "+jobInfo.RunnerName, err)
	}

	if err == nil && tray == nil {
		cs.poller.logger.Warnf("Tray %s not found for job %s (workflow run %d) — tray already removed",
			jobInfo.RunnerName, jobInfo.JobDisplayName, jobInfo.WorkflowRunID)
	}

	cs.poller.history.Add(&Message{
		Time:           time.Now(),
		Kind:           MessageKindJobStarted,
		TrayType:       cs.poller.trayType.Name,
		Repository:     fullRepoName(jobInfo.OwnerName, jobInfo.RepositoryName),
		WorkflowRunID:  jobInfo.WorkflowRunID,
		JobID:          jobID,
		JobDisplayName: jobInfo.JobDisplayName,
		RunnerName:     jobInfo.RunnerName,
	})

	return nil
}

func (cs *catteryScaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	cs.poller.logger.Infof("Job completed: %s on runner %s (result: %s)",
		jobInfo.JobDisplayName, jobInfo.RunnerName, jobInfo.Result)

	if jobInfo.RunnerName == "" {
		cs.poller.logger.Warnf("Job completed with empty runner name (result: %s, job: %s) — skipping tray deletion",
			jobInfo.Result, jobInfo.JobDisplayName)
		return nil
	}

	// Per-tray failure: log and count, do not fail the session. The stale
	// handler and the agent's own unregister will still remove the tray.
	if _, err := cs.poller.trayManager.DeleteTray(ctx, jobInfo.RunnerName); err != nil {
		cs.recordHandlerError("delete tray "+jobInfo.RunnerName, err)
	}

	jobID, _ := strconv.ParseInt(jobInfo.JobID, 10, 64)
	cs.poller.history.Add(&Message{
		Time:           time.Now(),
		Kind:           MessageKindJobCompleted,
		TrayType:       cs.poller.trayType.Name,
		Repository:     fullRepoName(jobInfo.OwnerName, jobInfo.RepositoryName),
		WorkflowRunID:  jobInfo.WorkflowRunID,
		JobID:          jobID,
		JobDisplayName: jobInfo.JobDisplayName,
		RunnerName:     jobInfo.RunnerName,
		Result:         jobInfo.Result,
	})

	return nil
}

// fullRepoName combines the separate OwnerName/RepositoryName message fields
// into the "owner/repo" form used in GitHub URLs.
func fullRepoName(owner, repo string) string {
	if owner == "" || repo == "" {
		return repo
	}
	return owner + "/" + repo
}

// parseWorkflowName extracts the workflow filename (without extension) from
// a JobWorkflowRef like "owner/repo/.github/workflows/ci.yml@refs/heads/main".
func parseWorkflowName(ref string) string {
	if ref == "" {
		return ""
	}
	// Strip @ref suffix
	if i := strings.LastIndex(ref, "@"); i != -1 {
		ref = ref[:i]
	}
	name := path.Base(ref)
	// Remove extension (.yml / .yaml)
	if ext := path.Ext(name); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}
