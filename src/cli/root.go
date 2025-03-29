package cli

import (
	"cattery/agent"
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "cattery",
	Short: "Github self-hosted runners scheduler",
}

func init() {
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(serverCmd)

	agentCmd.Flags().StringVarP(
		&agent.RunnerFolder,
		"runner-folder",
		"r",
		"",
		"Path to the folder containing the runner distribution",
	)
	agentCmd.MarkFlagRequired("runner-folder")

	agentCmd.Flags().StringVarP(
		&agent.CatteryServerUrl,
		"server-url",
		"s",
		"http://localhost:5137",
		"URL of the Cattery server",
	)
	agentCmd.MarkFlagRequired("server-url")

	agentCmd.Flags().StringVarP(
		&agent.AgentId,
		"agent-id",
		"i",
		"",
		"ID of the agent",
	)
	agentCmd.MarkFlagRequired("agent-id")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
