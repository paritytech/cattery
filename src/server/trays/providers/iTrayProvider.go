package providers

import "cattery/server/trays"

type ITrayProvider interface {

	// GetTray returns the tray with the given ID.
	GetTray(id string) (*trays.Tray, error)

	// ListTrays returns all trays.
	ListTrays() ([]*trays.Tray, error)

	// CreateTray creates a new tray.
	CreateTray(trayConfig map[string]string) (*trays.Tray, error)

	// CleanTray deletes the tray with the given ID.
	CleanTray(id string) error
}
