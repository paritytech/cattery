package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"
)

var (
	providersMu sync.RWMutex
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
	var trayType = config.Get().GetTrayType(trayTypeName)

	if trayType == nil {
		return nil, errors.New("tray type not found: " + trayTypeName)
	}

	return GetProvider(trayType.Provider)
}

func GetProvider(providerName string) (TrayProvider, error) {
	providersMu.RLock()
	if existingProvider, ok := providers[providerName]; ok {
		providersMu.RUnlock()
		return existingProvider, nil
	}
	providersMu.RUnlock()

	var result TrayProvider

	var p = config.Get().GetProvider(providerName)

	if p == nil {
		return nil, errors.New("no provider found for " + providerName)
	}

	var provider = *p

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

	providersMu.Lock()
	providers[providerName] = result
	providersMu.Unlock()

	return result, nil
}
