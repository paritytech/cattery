package trays

import (
	"cattery/lib/config"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Tray struct {
	Id             string `bson:"id"`
	TrayType       string `bson:"trayType"`
	trayTypeConfig config.TrayType

	GitHubOrgName  string     `bson:"gitHubOrgName"`
	GitHubRunnerId int64      `bson:"gitHubRunnerId"`
	JobRunId       int64      `bson:"jobRunId"`
	Status         TrayStatus `bson:"status"`
	StatusChanged  time.Time  `bson:"statusChanged"`
}

func NewTray(trayType config.TrayType) *Tray {

	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	id := hex.EncodeToString(b)

	var tray = &Tray{
		Id:             fmt.Sprintf("%s-%s", trayType.Name, id),
		TrayType:       trayType.Name,
		trayTypeConfig: trayType,
		Status:         TrayStatusCreating,
		GitHubOrgName:  trayType.GitHubOrg,
		JobRunId:       0,
	}

	return tray
}

func (tray *Tray) GetId() string {
	return tray.Id
}

func (tray *Tray) GetGitHubOrgName() string {
	return tray.GitHubOrgName
}

func (tray *Tray) GetTrayType() string {
	return tray.TrayType
}

func (tray *Tray) GetTrayConfig() config.TrayConfig {
	return config.AppConfig.GetTrayType(tray.TrayType).Config
}
