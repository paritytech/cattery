package providers

import (
	"cattery/lib/config"
	"errors"
	log "github.com/sirupsen/logrus"
)

var providers = make(map[string]ITrayProvider)

var logger = log.WithFields(log.Fields{
	"name": "trayProviderFactory",
})

func GetProvider(providerName string) (ITrayProvider, error) {

	if existingProvider, ok := providers[providerName]; ok {
		return existingProvider, nil
	}

	var result ITrayProvider

	var provider, ok = config.AppConfig.Providers[providerName]

	if !ok {
		var err = errors.New("No provider found for " + providerName)
		logger.Errorf(err.Error())
		return nil, err
	}

	switch provider["type"] {
	case "docker":
		result = NewDockerProvider(providerName, provider)
	case "google":
		result = NewGceProvider(providerName, provider)
	default:
		var errMsg = "Unknown provider: " + providerName
		logger.Errorf(errMsg)
		return nil, errors.New(errMsg)
	}

	providers[providerName] = result

	return result, nil
}
