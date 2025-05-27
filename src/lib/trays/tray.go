package trays

import (
	"cattery/lib/config"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Tray struct {
	id             string          `bson:"id"`
	trayType       string          `bson:"labels"`
	trayTypeConfig config.TrayType `bson:"-"`

	gitHubOrgName string     `bson:"githubOrgName"`
	JobRunId      int64      `bson:"jobRunId"`
	Status        TrayStatus `bson:"status"`
	StatusChanged time.Time  `bson:"statusChange"`
}

func NewTray(trayType config.TrayType) *Tray {

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)

	var tray = &Tray{
		id:             fmt.Sprintf("%s-%s", trayType.Name, id),
		trayType:       trayType.Name,
		trayTypeConfig: trayType,
		Status:         TrayStatusCreating,
		gitHubOrgName:  trayType.GitHubOrg,
		JobRunId:       0,
	}

	return tray
}

func (tray *Tray) Id() string {
	return tray.id
}

func (tray *Tray) GitHubOrgName() string {
	return tray.gitHubOrgName
}

func (tray *Tray) TypeName() string {
	return tray.trayTypeConfig.Name
}

func (tray *Tray) Provider() string {
	return tray.trayTypeConfig.Provider
}

func (tray *Tray) TrayType() string {
	return tray.trayType
}

func (tray *Tray) TrayConfig() config.TrayConfig {
	return tray.trayTypeConfig.Config
}

func (tray *Tray) RunnerGroupId() int64 {
	return tray.trayTypeConfig.RunnerGroupId
}

func (tray *Tray) Shutdown() bool {
	return tray.trayTypeConfig.Shutdown
}
