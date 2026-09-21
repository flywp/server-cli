package cmd

import (
	"fmt"

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
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := docker.RunCompose(baseCompose, "up", "-d"); err != nil {
			return fmt.Errorf("starting base services: %w", err)
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			return fmt.Errorf("checking status of base services: %w", err)
		}

		color.Green("Base services started successfully")
		return nil
	},
}

var baseStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop base services",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			return fmt.Errorf("stopping base services: %w", err)
		}

		color.Green("Base services stopped successfully")
		return nil
	},
}

var baseRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart base services",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := docker.RunCompose(baseCompose, "down"); err != nil {
			return fmt.Errorf("stopping base services: %w", err)
		}

		if err := docker.RunCompose(baseCompose, "up", "-d"); err != nil {
			return fmt.Errorf("starting base services: %w", err)
		}

		if err := docker.RunCompose(baseCompose, "ps"); err != nil {
			return fmt.Errorf("checking status of base services: %w", err)
		}

		color.Green("Base services restarted successfully")
		return nil
	},
}

func init() {
	baseCmd.AddCommand(baseStartCmd)
	baseCmd.AddCommand(baseStopCmd)
	baseCmd.AddCommand(baseRestartCmd)
	requireDocker(baseStartCmd, baseStopCmd, baseRestartCmd)

	rootCmd.AddCommand(baseCmd)
}
