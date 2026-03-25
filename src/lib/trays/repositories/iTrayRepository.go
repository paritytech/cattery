package repositories

import (
	"cattery/lib/trays"
	"context"
	"time"
)

type ITrayRepository interface {
	GetById(ctx context.Context, trayId string) (*trays.Tray, error)
	Save(ctx context.Context, tray *trays.Tray) error
	Delete(ctx context.Context, trayId string) error
	UpdateStatus(ctx context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string) (*trays.Tray, error)
	CountByTrayType(ctx context.Context, trayType string) (map[trays.TrayStatus]int, int, error)
	MarkRedundant(ctx context.Context, trayType string, limit int) ([]*trays.Tray, error)
	GetStale(ctx context.Context, d time.Duration) ([]*trays.Tray, error)
}
