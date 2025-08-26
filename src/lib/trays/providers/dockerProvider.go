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

func (d *DockerProvider) GetProviderName() string {
	return d.name
}

func (d *DockerProvider) GetTray(id string) (*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DockerProvider) ListTrays() ([]*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DockerProvider) RunTray(tray *trays.Tray) error {

	var containerName = tray.GetId()
	var image = tray.GetTrayConfig().Get("image")

	var dockerCommand = exec.Command("docker", "run", "-d", "--rm",
		"--add-host=host.docker.internal:host-gateway",
		"--name", containerName,
		image,
		"/action-runner/cattery/cattery", "agent", "-i", tray.GetId(), "-s", "http://host.docker.internal:5137", "--runner-folder", "/action-runner")

	d.logger.Info("Running docker command: ", dockerCommand.String())
	err := dockerCommand.Run()

	if err != nil {
		d.logger.Error("Error running docker command: ", err)
		return err
	}

	return nil
}

func (d *DockerProvider) CleanTray(tray *trays.Tray) error {
	var dockerCommand = exec.Command("docker", "container", "stop", tray.GetId())
	dockerCommandOutput, err := dockerCommand.CombinedOutput()
	if err != nil {
		if strings.Contains(string(dockerCommandOutput), "no such container") {
			d.logger.Trace("No such container: ", tray.GetId())
			return nil
		}
		return err
	}

	return nil
}
