package providers

import (
	"cattery/lib/trays"
)

type TrayProvider interface {
	GetProviderName() string

	// RunTray spawns a new tray.
	RunTray(tray *trays.Tray) error

	// CleanTray deletes the tray with the given ID.
	CleanTray(tray *trays.Tray) error
}

// TrayProviderFactory resolves providers by name or by tray.
type TrayProviderFactory interface {
	GetProvider(providerName string) (TrayProvider, error)
	GetProviderForTray(tray *trays.Tray) (TrayProvider, error)
}
