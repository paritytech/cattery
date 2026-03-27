package scaleSetClient

import (
	"cattery/lib/config"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/actions/scaleset"
	log "github.com/sirupsen/logrus"
)

type ScaleSetClient struct {
	client   *scaleset.Client
	session  *scaleset.MessageSessionClient
	scaleSet *scaleset.RunnerScaleSet
	org      *config.GitHubOrganization
	trayType *config.TrayType
	logger   *log.Entry
}

func NewScaleSetClient(org *config.GitHubOrganization, trayType *config.TrayType) (*ScaleSetClient, error) {
	privateKey, err := os.ReadFile(org.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	client, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: fmt.Sprintf("https://github.com/%s", org.Name),
		GitHubAppAuth: scaleset.GitHubAppAuth{
			ClientID:       org.AppClientId,
			InstallationID: org.InstallationId,
			PrivateKey:     string(privateKey),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create scale set client: %w", err)
	}

	return &ScaleSetClient{
		client:   client,
		org:      org,
		trayType: trayType,
		logger: log.WithFields(log.Fields{
			"component": "scaleSetClient",
			"trayType":  trayType.Name,
			"org":       org.Name,
		}),
	}, nil
}

func (sc *ScaleSetClient) EnsureScaleSet(ctx context.Context) error {
	existing, err := sc.client.GetRunnerScaleSet(ctx, int(sc.trayType.RunnerGroupId), sc.trayType.Name)
	if err != nil {
		return fmt.Errorf("failed to get scale set: %w", err)
	}
	if existing != nil {
		sc.scaleSet = existing
		sc.logger.Infof("Found existing scale set: %s (ID: %d)", existing.Name, existing.ID)
		return nil
	}

	sc.logger.Infof("Creating new scale set: %s", sc.trayType.Name)
	created, err := sc.client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          sc.trayType.Name,
		RunnerGroupID: int(sc.trayType.RunnerGroupId),
		Labels: []scaleset.Label{
			{Name: sc.trayType.Name, Type: "User"},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create scale set: %w", err)
	}

	sc.scaleSet = created
	sc.logger.Infof("Created scale set: %s (ID: %d)", created.Name, created.ID)
	return nil
}

func (sc *ScaleSetClient) CreateSession(ctx context.Context) error {
	hostname, _ := os.Hostname()

	const maxRetries = 5
	const retryDelay = 30 * time.Second

	for attempt := range maxRetries {
		session, err := sc.client.MessageSessionClient(ctx, sc.scaleSet.ID, hostname)
		if err == nil {
			sc.session = session
			sc.logger.Info("Message session created")
			return nil
		}

		if !strings.Contains(err.Error(), "409 Conflict") || attempt == maxRetries-1 {
			return fmt.Errorf("failed to create message session: %w", err)
		}

		sc.logger.Warnf("Session conflict (attempt %d/%d), stale session likely exists — retrying in %v", attempt+1, maxRetries, retryDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}

	return fmt.Errorf("unreachable")
}

func (sc *ScaleSetClient) Poll(ctx context.Context, lastMessageID int, maxCapacity int) (*scaleset.RunnerScaleSetMessage, error) {
	return sc.session.GetMessage(ctx, lastMessageID, maxCapacity)
}

func (sc *ScaleSetClient) Ack(ctx context.Context, messageID int) error {
	return sc.session.DeleteMessage(ctx, messageID)
}

func (sc *ScaleSetClient) GenerateJitRunnerConfig(ctx context.Context, runnerName string) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return sc.client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name:       runnerName,
		WorkFolder: "_work",
	}, sc.scaleSet.ID)
}

func (sc *ScaleSetClient) Close(ctx context.Context) error {
	if sc.session != nil {
		return sc.session.Close(ctx)
	}
	return nil
}

func (sc *ScaleSetClient) GetScaleSetID() int {
	if sc.scaleSet != nil {
		return sc.scaleSet.ID
	}
	return 0
}

func (sc *ScaleSetClient) Session() scaleset.RunnerScaleSetSession {
	if sc.session != nil {
		return sc.session.Session()
	}
	return scaleset.RunnerScaleSetSession{}
}
