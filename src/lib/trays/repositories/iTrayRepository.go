package repositories

import "cattery/lib/trays"

type ITrayRepository interface {
	GetById(trayId string) (*trays.Tray, error)
	Save(tray *trays.Tray) error
	Delete(trayId string) error
	UpdateStatus(trayId string, status trays.TrayStatus, jobRunId int64) (*trays.Tray, error)
	CountByTrayType(trayType string) (int, error)
}
