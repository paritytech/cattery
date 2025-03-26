package providers

import (
	"cattery/lib/trays"
)

type ITrayProvider interface {

	// GetTray returns the tray with the given ID.
	GetTray(id string) (*trays.Tray, error)

	// ListTrays returns all trays.
	ListTrays() ([]*trays.Tray, error)

	// RunTray spawns a new tray.
	RunTray(tray *trays.Tray) error

	// CleanTray deletes the tray with the given ID.
	CleanTray(id string) error
}
