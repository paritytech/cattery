package config

type TrayConfig interface {
}

type GoogleTrayConfig struct {
	TrayConfig
	Project          string   `yaml:"project"`
	Zones            []string `yaml:"zone"`
	MachineType      string   `yaml:"machineType"`
	InstanceTemplate string   `yaml:"instanceTemplate"`
	NamePrefix       string   `yaml:"namePrefix"`
}

type DockerTrayConfig struct {
	TrayConfig
	Image      string `yaml:"image"`
	NamePrefix string `yaml:"namePrefix"`
}
