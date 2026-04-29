package repositories

import (
	"cattery/lib/trays"
	"context"
	"time"
)

type TrayRepository interface {
	GetById(ctx context.Context, trayId string) (*trays.Tray, error)
	List(ctx context.Context) ([]*trays.Tray, error)
	Save(ctx context.Context, tray *trays.Tray) error
	Delete(ctx context.Context, trayId string) error
	UpdateStatus(ctx context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string, jobName string, workflowName string) (*trays.Tray, error)
	// SetProviderData writes the supplied keys into the row's providerData map
	// (via $set on each providerData.<key>) without modifying status or other
	// fields. Returns the row as it exists after the write, or (nil, nil) if
	// the row is missing.
	SetProviderData(ctx context.Context, trayId string, data map[string]string) (*trays.Tray, error)
	CountActive(ctx context.Context, trayType string) (int, error)
	// GetStale returns trays whose status is a key in thresholds and whose
	// statusChanged is older than the corresponding duration. A status absent
	// from the map is not checked.
	GetStale(ctx context.Context, thresholds map[trays.TrayStatus]time.Duration) ([]*trays.Tray, error)
}
