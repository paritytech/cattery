package trays

import (
	"cattery/lib/config"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Tray struct {
	Id           string `bson:"id"`
	TrayTypeName string `bson:"trayTypeName"`

	ProviderName   string     `bson:"providerName"`
	GitHubOrgName  string     `bson:"gitHubOrgName"`
	GitHubRunnerId int64      `bson:"gitHubRunnerId"`
	JobRunId       int64      `bson:"jobRunId"`
	WorkflowRunId  int64      `bson:"workflowRunId"`
	Repository     string     `bson:"repository"`
	Status         TrayStatus `bson:"status"`
	StatusChanged  time.Time  `bson:"statusChanged"`

	ProviderData map[string]string `bson:"providerData"`
}

func NewTray(trayType config.TrayType) *Tray {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	id := hex.EncodeToString(b)

	return &Tray{
		Id:            fmt.Sprintf("%s-%s", trayType.Name, id),
		TrayTypeName:  trayType.Name,
		ProviderName:  trayType.Provider,
		Status:        TrayStatusCreating,
		GitHubOrgName: trayType.GitHubOrg,
		ProviderData:  make(map[string]string),
	}
}

// TrayType returns the configuration for this tray's type from the current config.
// Returns nil if the tray type no longer exists in config.
func (tray *Tray) TrayType() *config.TrayType {
	return config.AppConfig.GetTrayType(tray.TrayTypeName)
}

// TrayConfig returns the provider-specific config (DockerTrayConfig, GoogleTrayConfig, etc.).
// Returns nil if the tray type no longer exists in config.
func (tray *Tray) TrayConfig() config.TrayConfig {
	tt := tray.TrayType()
	if tt == nil {
		return nil
	}
	return tt.Config
}

func (tray *Tray) String() string {
	return fmt.Sprintf("id: %s, trayTypeName: %s, status: %s, gitHubOrgName: %s, statusChanged: %s",
		tray.Id, tray.TrayTypeName, tray.Status, tray.GitHubOrgName, tray.StatusChanged.Format(time.RFC3339))
}
