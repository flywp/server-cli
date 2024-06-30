package cmd

import (
	"fmt"
	"os"

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
			fmt.Println("Error starting base services:", err)
			os.Exit(1)
			return
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			fmt.Println("Error checking status of base services:", err)
		}

		fmt.Println("Base services started successfully")
	},
}

var baseStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop base services",
	Run: func(cmd *cobra.Command, args []string) {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			fmt.Println("Error stopping base services:", err)
		}

		fmt.Println("Base services stopped successfully")
	},
}

var baseRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart base services",
	Run: func(cmd *cobra.Command, args []string) {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			fmt.Println("Error stopping base services:", err)
		}

		if err := docker.RunCompose(baseCompose, "up", "-d"); err != nil {
			fmt.Println("Error starting base services:", err)
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			fmt.Println("Error checking status of base services:", err)
		}

		fmt.Println("Base services restarted successfully")
	},
}

func init() {
	baseCmd.AddCommand(baseStartCmd)
	baseCmd.AddCommand(baseStopCmd)
	baseCmd.AddCommand(baseRestartCmd)

	rootCmd.AddCommand(baseCmd)
}
