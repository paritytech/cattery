package providers

import "cattery/lib/config"

func GetProvider(providerName string) ITrayProvider {

	switch providerName {
	case "docker":
		var dockerProvider = NewDockerProvider(config.DockerConfig{})
		return dockerProvider
	case "google":
		// TODO implement me
		panic("implement me")
	default:
		panic("Unknown provider: " + providerName)
	}

}
