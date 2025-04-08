package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
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

	var containerName = tray.Id()
	var image = tray.TrayConfig().Get("image")

	var dockerCommand = exec.Command("docker", "run", "-d", "--rm", "--name", containerName, image, "cattery", "-i", tray.Id(), "-s", d.config.Get("catteryUrl"))
	err := dockerCommand.Run()
	log.Info("Running docker command: ", dockerCommand.String())

	if err != nil {
		d.logger.Error("Error running docker command: ", err)
		return err
	}

	return nil
}

func (d DockerProvider) CleanTray(tray *trays.Tray) error {
	var dockerCommand = exec.Command("docker", "container", "stop", tray.Id())
	dockerCommandOutput, err := dockerCommand.CombinedOutput()
	if err != nil {
		if strings.Contains(string(dockerCommandOutput), "no such container") {
			d.logger.Trace("No such container: ", tray.Id())
			return nil
		}
		return err
	}

	return nil
}
