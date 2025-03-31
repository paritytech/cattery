package cmd

import (
	"cattery/lib/config"
	"cattery/server"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Cattery server",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {

		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		if configPath == "" {
			viper.AddConfigPath("/etc/cattery/")
		} else {
			viper.SetConfigFile(configPath)
		}

		err := viper.ReadInConfig()
		if err != nil {
			var configFileNotFoundError viper.ConfigFileNotFoundError
			if errors.As(err, &configFileNotFoundError) {
				fmt.Println("Config file not found")
				os.Exit(1)
				return
			} else {
				panic(fmt.Errorf("fatal error config file: %w", err))
			}
		}

		err = viper.Unmarshal(&config.AppConfig)
		if err != nil {
			panic(fmt.Errorf("fatal error unmarshaling config file: %w", err))
		}

	},
	Run: func(cmd *cobra.Command, args []string) {
		server.Start()
	},
}
