package config

var AppConfig = &CatteryConfig{}

type CatteryConfig struct {
	ListenAddress  string
	AppID          int64
	InstallationId int64
	PrivateKeyPath string

	Providers struct {
		Docker *DockerConfig
		Google *GoogleConfig
	}

	TrayTypes map[string]TrayType
}

type TrayType struct {
	Provider   string
	TrayConfig map[string]string
}

type DockerConfig struct {
	Image string
}

type GoogleConfig struct {
}
