package trays

import (
	"cattery/lib/config"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type Tray struct {
	id       string
	labels   []string
	trayType config.TrayType

	JobRunId int64
}

func NewTray(
	labels []string,
	trayType config.TrayType) *Tray {

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)

	var tray = &Tray{
		id:       fmt.Sprintf("%s-%s", trayType.Name, id),
		labels:   labels,
		trayType: trayType,
	}

	return tray
}

func (tray *Tray) Id() string {
	return tray.id
}

func (tray *Tray) GitHubOrgName() string {
	return tray.trayType.GitHubOrg
}

func (tray *Tray) TypeName() string {
	return tray.trayType.Name
}

func (tray *Tray) Provider() string {
	return tray.trayType.Provider
}

func (tray *Tray) Labels() []string {
	return tray.labels
}

func (tray *Tray) TrayConfig() config.TrayConfig {
	return tray.trayType.Config
}

func (tray *Tray) RunnerGroupId() int64 {
	return tray.trayType.RunnerGroupId
}

func (tray *Tray) Shutdown() bool {
	return tray.trayType.Shutdown
}
