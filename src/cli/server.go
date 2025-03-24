package cli

import (
	"Cattery/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Cattery server",
	Run: func(cmd *cobra.Command, args []string) {
		server.Start()
	},
}
