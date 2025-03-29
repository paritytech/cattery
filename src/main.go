package main

import (
	"cattery/cli"
	"cattery/lib/config"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func main() {

	//var p = providers.NewGceProvider()
	//
	//errr := p.RunTray(&trays.Tray{
	//	Id:         "1122334455",
	//	Name:       "catterey-test-1122334455",
	//	Address:    "",
	//	Type:       "gce",
	//	Provider:   "gce",
	//	Labels:     nil,
	//	TrayConfig: nil,
	//})
	//if errr != nil {
	//	return
	//}

	log.SetLevel(log.DebugLevel)

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/appname/")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			log.Warn("Config file not found; using defaults")
		} else {
			panic(fmt.Errorf("fatal error config file: %w", err))
		}
	}

	err = viper.Unmarshal(&config.AppConfig)
	if err != nil {
		panic(fmt.Errorf("fatal error unmarshaling config file: %w", err))
	}

	cli.Execute()
}
