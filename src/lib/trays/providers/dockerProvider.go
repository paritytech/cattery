package providers

import (
	"cattery/lib/bootstrap"
	"cattery/lib/config"
	"cattery/lib/trays"
	"fmt"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
)

type DockerProvider struct {
	name   string
	config config.ProviderConfig

	logger *log.Entry
}

func NewDockerProvider(name string, providerConfig config.ProviderConfig) *DockerProvider {
	return &DockerProvider{
		name:   name,
		config: providerConfig,
		logger: log.WithFields(log.Fields{
			"name":         "DockerProvider",
			"providerName": name,
			"providerType": "docker",
		}),
	}
}

func (d *DockerProvider) GetProviderName() string {
	return d.name
}

func (d *DockerProvider) RunTray(tray *trays.Tray) error {

	containerName := tray.Id

	trayConfig, ok := tray.TrayConfig().(config.DockerTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for docker provider, tray %s", tray.Id)
	}

	image := trayConfig.Image
	serverUrl := config.Get().Server.AdvertiseUrl

	var bootstrapCfg config.BootstrapConfig
	if tt := tray.TrayType(); tt != nil {
		bootstrapCfg = tt.Bootstrap
	}

	if bootstrapCfg.Enabled {
		return d.runWithBootstrap(tray, containerName, image, serverUrl, bootstrapCfg)
	}

	dockerCommand := exec.Command("docker", "run", "-d", "--rm",
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

// runWithBootstrap launches a container that bootstraps the cattery agent at
// startup instead of relying on a pre-baked binary in the image. The script is
// piped to the container's shell via stdin, which avoids quote-escaping
// headaches with `-c "..."` for multiline scripts.
func (d *DockerProvider) runWithBootstrap(tray *trays.Tray, containerName, image, serverUrl string, cfg config.BootstrapConfig) error {
	script, err := bootstrap.Generate(cfg, bootstrap.Params{
		ServerURL: serverUrl,
		AgentID:   tray.Id,
	})
	if err != nil {
		return fmt.Errorf("generate bootstrap script: %w", err)
	}

	dockerCommand := exec.Command("docker", "run", "-d", "--rm", "-i",
		"--add-host=host.docker.internal:host-gateway",
		"--name", containerName,
		"--entrypoint", "/bin/sh",
		image,
		"-s",
	)
	dockerCommand.Stdin = strings.NewReader(script)

	d.logger.Info("Running docker bootstrap command: ", dockerCommand.String())
	if err := dockerCommand.Run(); err != nil {
		d.logger.Error("Failed to run docker bootstrap command: ", err)
		return err
	}
	return nil
}

func (d *DockerProvider) CleanTray(tray *trays.Tray) error {
	dockerCommand := exec.Command("docker", "container", "stop", tray.Id)
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
