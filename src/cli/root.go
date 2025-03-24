package cli

import (
	"Cattery/agent"
	"Cattery/server"
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

	serverCmd.Flags().Int64Var(
		&server.AppId,
		"app-id",
		0,
		"Github App ID",
	)
	serverCmd.MarkFlagRequired("app-id")

	serverCmd.Flags().Int64Var(
		&server.InstallationId,
		"installation-id",
		0,
		"Github Installation ID",
	)
	serverCmd.MarkFlagRequired("installation-id")

	serverCmd.Flags().StringVarP(
		&server.PrivateKeyPath,
		"private-key-path",
		"k",
		"",
		"Path to the private key file",
	)
	serverCmd.MarkFlagRequired("private-key-path")

}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
