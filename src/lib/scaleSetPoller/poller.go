package scaleSetPoller

import (
	"cattery/lib/metrics"
	"cattery/lib/scaleSetClient"
	"cattery/lib/trayManager"
	"context"
	"fmt"
	"strconv"
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
		logger: log.WithFields(log.Fields{
			"component": "scaleSetPoller",
			"trayType":  trayType.Name,
		}),
	}
}

func (p *Poller) Client() *scaleSetClient.ScaleSetClient {
	return p.client
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

func (s *sessionAdapter) Session() scaleset.RunnerScaleSetSession {
	return s.client.Session()
}

// catteryScaler implements the listener.Scaler and listener.MetricsRecorder interfaces.
type catteryScaler struct {
	poller      *Poller
	latestStats *scaleset.RunnerScaleSetStatistic
}

// MetricsRecorder implementation — captures GitHub statistics for ghost tray detection.

func (cs *catteryScaler) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	cs.latestStats = statistics
}

func (cs *catteryScaler) RecordJobStarted(msg *scaleset.JobStarted)     {}
func (cs *catteryScaler) RecordJobCompleted(msg *scaleset.JobCompleted) {}
func (cs *catteryScaler) RecordDesiredRunners(count int)                {}

func (cs *catteryScaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	githubIdleRunners := 0
	if cs.latestStats != nil {
		githubIdleRunners = cs.latestStats.TotalIdleRunners
	}

	err := cs.poller.trayManager.ScaleForDemand(ctx, cs.poller.trayType, count, githubIdleRunners)
	if err != nil {
		cs.poller.logger.Errorf("Failed to scale for demand (%d): %v", count, err)
		return 0, err
	}

	total, err := cs.poller.trayManager.CountTrays(ctx, cs.poller.trayType.Name)
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (cs *catteryScaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	cs.poller.logger.Infof("Job started: %s on runner %s (workflow run %d)",
		jobInfo.JobDisplayName, jobInfo.RunnerName, jobInfo.WorkflowRunID)

	jobID, _ := strconv.ParseInt(jobInfo.JobID, 10, 64)

	_, err := cs.poller.trayManager.SetJob(ctx, jobInfo.RunnerName, jobID, jobInfo.WorkflowRunID, jobInfo.RepositoryName)
	if err != nil {
		cs.poller.logger.Errorf("Failed to set job on tray %s: %v", jobInfo.RunnerName, err)
		return err
	}

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

	_, err := cs.poller.trayManager.DeleteTray(ctx, jobInfo.RunnerName)
	if err != nil {
		cs.poller.logger.Errorf("Failed to delete tray %s: %v", jobInfo.RunnerName, err)
		return err
	}

	metrics.RegisteredTraysAdd(cs.poller.trayType.GitHubOrg, cs.poller.trayType.Name, -1)
	return nil
}
