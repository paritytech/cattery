package repositories

import "cattery/lib/trays"

type ITrayRepository interface {
	Get(trayId string) (*trays.Tray, error)
	Save(tray *trays.Tray) error
	Delete(trayId string) error
	UpdateStatus(trayId string, status trays.TrayStatus, jobRunId int64) (*trays.Tray, error)
	CountByTrayType(trayType string) (int64, error)
}
