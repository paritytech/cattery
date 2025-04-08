package cmd

import (
	"cattery/lib/config"
	"cattery/server"
	"github.com/spf13/cobra"
	"os"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Cattery server",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {

		_, err := config.LoadConfig(&configPath)
		if err != nil {
			cmd.PrintErrln("Error loading config:", err)
			os.Exit(1)
			return
		}

	},
	Run: func(cmd *cobra.Command, args []string) {
		server.Start()
	},
}
