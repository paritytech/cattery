package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	log "github.com/sirupsen/logrus"
	"os/exec"
)

type DockerProvider struct {
	ITrayProvider
	name   string
	config config.ProviderConfig

	logger *log.Entry
}

func NewDockerProvider(name string, providerConfig config.ProviderConfig) *DockerProvider {
	var provider = &DockerProvider{}

	provider.name = name
	provider.config = providerConfig

	provider.logger = log.WithFields(log.Fields{
		"name":         "DockerProvider",
		"providerName": name,
		"providerType": "docker",
	})

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

func (d DockerProvider) RunTray(tray *trays.Tray) error {

	logger.Debugf("Create tray %s", tray.Id)

	var containerName = tray.Name
	var image = tray.TrayConfig.Get("image")
	var dockerCommand = exec.Command("docker", "run", "-d", "--rm", "--hostname", containerName, "--name", containerName, image)

	err := dockerCommand.Run()
	if err != nil {
		return err
	}

	return nil
}

func (d DockerProvider) CleanTray(id string) error {
	return nil
}
