package trays

import "cattery/lib/config"

type Tray struct {
	Id         string
	Name       string // Name of the tray, may be hostname of vm
	Address    string
	Type       string
	Provider   string
	Labels     []string
	TrayConfig config.TrayConfig
}
