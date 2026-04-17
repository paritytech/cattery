package providers

import (
	"cattery/lib/trays"
	"context"
)

type TrayProvider interface {
	GetProviderName() string

	// RunTray spawns a new tray.
	RunTray(ctx context.Context, tray *trays.Tray) error

	// CleanTray deletes the tray with the given ID.
	CleanTray(ctx context.Context, tray *trays.Tray) error
}

// TrayProviderFactory resolves providers by name or by tray.
type TrayProviderFactory interface {
	GetProvider(providerName string) (TrayProvider, error)
	GetProviderForTray(tray *trays.Tray) (TrayProvider, error)
}
