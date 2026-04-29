package providers

import (
	"cattery/lib/trays"
	"context"
)

// TrayProvider models a two-phase deployment lifecycle:
//
//   - StartDeploy submits the create request to the upstream provider and
//     populates tray.ProviderData with whatever the cleanup path needs (zone,
//     resource ID, etc.). It must return as soon as the provider has accepted
//     the request, NOT after the resource is fully ready. If StartDeploy
//     returns an error, it must still populate ProviderData with whatever
//     cleanup data is known, since the resource may have been partially
//     created.
//
//   - WaitDeploy blocks until the resource is reachable. May be interrupted by
//     ctx cancellation. Some providers (e.g. local Docker) may treat this as a
//     no-op.
//
//   - CleanTray removes the upstream resource using only fields stored in
//     tray.ProviderData. It MUST be safe to call on a tray that StartDeploy
//     never finished — i.e. given partial ProviderData.
type TrayProvider interface {
	GetProviderName() string

	StartDeploy(ctx context.Context, tray *trays.Tray) error
	WaitDeploy(ctx context.Context, tray *trays.Tray) error
	CleanTray(ctx context.Context, tray *trays.Tray) error
}

// TrayProviderFactory resolves providers by name or by tray.
type TrayProviderFactory interface {
	GetProvider(providerName string) (TrayProvider, error)
	GetProviderForTray(tray *trays.Tray) (TrayProvider, error)
}
