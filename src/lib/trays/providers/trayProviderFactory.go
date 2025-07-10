package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"errors"
	log "github.com/sirupsen/logrus"
)

var providers = make(map[string]ITrayProvider)

var logger = log.WithFields(log.Fields{
	"name": "trayProviderFactory",
})

func GetProviderForTray(tray *trays.Tray) (ITrayProvider, error) {
	return GetProviderByTrayTypeName(tray.TrayTypeName)
}

func GetProviderByTrayTypeName(trayTypeName string) (ITrayProvider, error) {
	var trayType = config.AppConfig.GetTrayType(trayTypeName)

	if trayType == nil {
		return nil, errors.New("tray type not found: " + trayTypeName)
	}

	return GetProvider(trayType.Provider)
}

func GetProvider(providerName string) (ITrayProvider, error) {

	if existingProvider, ok := providers[providerName]; ok {
		return existingProvider, nil
	}

	var result ITrayProvider

	var p = config.AppConfig.GetProvider(providerName)

	if p == nil {
		var err = errors.New("No provider found for " + providerName)
		logger.Error(err.Error())
		return nil, err
	}

	var provider = *p

	switch provider["type"] {
	case "docker":
		result = NewDockerProvider(providerName, provider)
	case "google":
		result = NewGceProvider(providerName, provider)
	default:
		var errMsg = "Unknown provider: " + providerName
		logger.Error(errMsg)
		return nil, errors.New(errMsg)
	}

	providers[providerName] = result

	return result, nil
}
