package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"
)

var (
	providersMu sync.Mutex
	providers   = make(map[string]TrayProvider)
)

var logger = log.WithFields(log.Fields{
	"name": "trayProviderFactory",
})

// DefaultFactory is the standard provider factory backed by config.
type DefaultFactory struct{}

func (DefaultFactory) GetProvider(providerName string) (TrayProvider, error) {
	return GetProvider(providerName)
}

func (DefaultFactory) GetProviderForTray(tray *trays.Tray) (TrayProvider, error) {
	return GetProviderForTray(tray)
}

func GetProviderForTray(tray *trays.Tray) (TrayProvider, error) {
	return GetProviderByTrayTypeName(tray.TrayTypeName)
}

func GetProviderByTrayTypeName(trayTypeName string) (TrayProvider, error) {
	trayType := config.Get().GetTrayType(trayTypeName)

	if trayType == nil {
		return nil, errors.New("tray type not found: " + trayTypeName)
	}

	return GetProvider(trayType.Provider)
}

func GetProvider(providerName string) (TrayProvider, error) {
	providersMu.Lock()
	defer providersMu.Unlock()

	if existingProvider, ok := providers[providerName]; ok {
		return existingProvider, nil
	}

	p := config.Get().GetProvider(providerName)
	if p == nil {
		return nil, errors.New("no provider found for " + providerName)
	}

	provider := *p

	var result TrayProvider
	switch provider["type"] {
	case "docker":
		if p := NewDockerProvider(providerName, provider); p != nil {
			result = p
		}
	case "google":
		if p := NewGceProvider(providerName, provider); p != nil {
			result = p
		}
	default:
		return nil, errors.New("unknown provider type: " + provider["type"])
	}

	if result == nil {
		return nil, errors.New("failed to initialize provider: " + providerName)
	}

	providers[providerName] = result
	return result, nil
}
