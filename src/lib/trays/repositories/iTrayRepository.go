package repositories

import (
	"cattery/lib/trays"
	"time"
)

type ITrayRepository interface {
	GetById(trayId string) (*trays.Tray, error)
	Save(tray *trays.Tray) error
	Delete(trayId string) error
	UpdateStatus(trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64) (*trays.Tray, error)
	CountByTrayType(trayType string) (map[trays.TrayStatus]int, int, error)
	MarkRedundant(trayType string, limit int) ([]*trays.Tray, error)
	GetStale(d time.Duration, rd time.Duration) ([]*trays.Tray, error)
}
