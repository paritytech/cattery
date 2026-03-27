package providers

import (
	"cattery/lib/trays"
)

type ITrayProvider interface {
	GetProviderName() string

	// RunTray spawns a new tray.
	RunTray(tray *trays.Tray) error

	// CleanTray deletes the tray with the given ID.
	CleanTray(tray *trays.Tray) error
}
