package providers

import (
	"cattery/lib/config"
	"cattery/server/trays"
)

type DockerProvider struct {
	ITrayProvider
	config config.DockerConfig
}

func NewDockerProvider(config config.DockerConfig) *DockerProvider {
	var provider = &DockerProvider{}
	provider.config = config
	return provider
}

func (d DockerProvider) GetTray(id string) (*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (d DockerProvider) ListTrays() ([]*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (d DockerProvider) CreateTray(trayConfig map[string]string) (*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (d DockerProvider) CleanTray(id string) error {
	//TODO implement me
	panic("implement me")
}
