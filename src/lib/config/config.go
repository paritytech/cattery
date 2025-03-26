package config

import "strings"

var AppConfig = &CatteryConfig{}

type CatteryConfig struct {
	ListenAddress  string
	AppID          int64
	InstallationId int64
	PrivateKeyPath string
	WebhookSecret  string

	Providers map[string]ProviderConfig

	TrayTypes map[string]TrayType
}

type TrayType struct {
	Provider string
	Config   TrayConfig
}

type TrayConfig map[string]string

func (t TrayConfig) Get(key string) string {
	return t[strings.ToLower(key)]
}

type ProviderConfig map[string]string

func (p ProviderConfig) Get(key string) string {
	return p[strings.ToLower(key)]
}
