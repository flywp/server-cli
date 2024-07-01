package cmd

import (
	"os"

	"github.com/fatih/color"
	"github.com/flywp/server-cli/internal/docker"
	"github.com/spf13/cobra"
)

const baseCompose = "/home/fly/.fly/docker-compose.yml"

var baseCmd = &cobra.Command{
	Use:   "base",
	Short: "Manage base services",
}

var baseStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start base services",
	Run: func(cmd *cobra.Command, args []string) {
		if err := docker.RunCompose(baseCompose, "up", "-d"); err != nil {
			color.Red("Error starting base services: %v", err)
			os.Exit(1)
			return
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			color.Red("Error checking status of base services: %v", err)
		}

		color.Green("Base services started successfully")
	},
}

var baseStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop base services",
	Run: func(cmd *cobra.Command, args []string) {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			color.Red("Error stopping base services:", err)
		}

		color.Green("Base services stopped successfully")
	},
}

var baseRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart base services",
	Run: func(cmd *cobra.Command, args []string) {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			color.Red("Error stopping base services:", err)
		}

		if err := docker.RunCompose(baseCompose, "up", "-d"); err != nil {
			color.Red("Error starting base services:", err)
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			color.Red("Error checking status of base services:", err)
		}

		color.Green("Base services restarted successfully")
	},
}

func init() {
	baseCmd.AddCommand(baseStartCmd)
	baseCmd.AddCommand(baseStopCmd)
	baseCmd.AddCommand(baseRestartCmd)

	rootCmd.AddCommand(baseCmd)
}
