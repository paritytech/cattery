package providers

import "cattery/lib/config"

var providers = make(map[string]ITrayProvider)

func GetProvider(providerName string) ITrayProvider {

	if provider, ok := providers[providerName]; ok {
		return provider
	}

	var result ITrayProvider

	switch providerName {
	case "docker":
		result = NewDockerProvider(config.DockerConfig{})
	case "google":
		// TODO implement me
		panic("implement me")
	default:
		panic("Unknown provider: " + providerName)
	}

	providers[providerName] = result

	return result
}
