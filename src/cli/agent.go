package cli

import (
	"Cattery/agent"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start the Cattery agent",
	Run: func(cmd *cobra.Command, args []string) {
		agent.Start()
	},
}
