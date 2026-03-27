package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"fmt"
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

func (d *DockerProvider) RunTray(tray *trays.Tray) error {

	var containerName = tray.Id

	trayConfig, ok := tray.TrayConfig().(config.DockerTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for docker provider, tray %s", tray.Id)
	}

	var image = trayConfig.Image

	var serverUrl = config.AppConfig.Server.AdvertiseUrl

	var dockerCommand = exec.Command("docker", "run", "-d", "--rm",
		"--add-host=host.docker.internal:host-gateway",
		"--name", containerName,
		image,
		"/action-runner/cattery/cattery", "agent", "-i", tray.Id, "-s", serverUrl, "--runner-folder", "/action-runner")

	d.logger.Info("Running docker command: ", dockerCommand.String())
	err := dockerCommand.Run()

	if err != nil {
		d.logger.Error("Failed to run docker command: ", err)
		return err
	}

	return nil
}

func (d *DockerProvider) CleanTray(tray *trays.Tray) error {
	var dockerCommand = exec.Command("docker", "container", "stop", tray.Id)
	dockerCommandOutput, err := dockerCommand.CombinedOutput()
	if err != nil {
		output := string(dockerCommandOutput)
		d.logger.Trace(output)

		if strings.Contains(strings.ToLower(output), "no such container") {
			d.logger.Trace("No such container: ", tray.Id)
			return nil
		}
		return err
	}

	return nil
}
