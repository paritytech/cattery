package trays

import (
	"cattery/lib/config"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type Tray struct {
	id         string
	trayType   string
	provider   string
	labels     []string
	trayConfig config.TrayConfig

	JobRunId int64
}

func NewTray(labels []string, trayTypeName string, trayType config.TrayType) *Tray {

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)

	var tray = &Tray{
		id:         fmt.Sprintf("%s-%s", trayTypeName, id),
		trayType:   trayTypeName,
		provider:   trayType.Provider,
		labels:     labels,
		trayConfig: trayType.Config,
	}

	return tray
}

func (tray *Tray) Id() string {
	return tray.id
}

func (tray *Tray) Type() string {
	return tray.trayType
}

func (tray *Tray) Provider() string {
	return tray.provider
}

func (tray *Tray) Labels() []string {
	return tray.labels
}

func (tray *Tray) TrayConfig() config.TrayConfig {
	return tray.trayConfig
}
