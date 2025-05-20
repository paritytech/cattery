package repositories

import "cattery/lib/trays"

type ITrayRepository interface {
	Get(trayId string) (*trays.Tray, error)
	Save(tray *trays.Tray) error
	Delete(trayId string) error
	GetGroupByLabels() map[string][]*trays.Tray
	Len() int
}
