package cmd

import (
	"os"

	"github.com/fatih/color"
	"github.com/flywp/server-cli/internal/docker"
	"github.com/flywp/server-cli/internal/utils"
	"github.com/spf13/cobra"
)

var wpCmd = &cobra.Command{
	Use:   "wp",
	Short: "Run wp-cli commands",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			return
		}

		if err := docker.RunWPCLI(composePath, args); err != nil {
			color.Red("Error running wp-cli: %s", err)
		}
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the site",
	Long:  "Start the Docker container for the site",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			return
		}

		if err := docker.RunCompose(composePath, "up", "-d"); err != nil {
			color.Red("Error starting container:", err)
		}
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the site",
	Long:  "Stop the Docker container for the site",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			return
		}

		if err := docker.RunCompose(composePath, "down"); err != nil {
			color.Red("Error stopping container:", err)
		}
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart [container]",
	Short: "Restart the site or a specific container",
	Long:  "Restart the Docker containers for the site. Optionally, specify a container to restart only that container.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			return
		}

		if len(args) == 1 {
			containerName := args[0]
			if err := docker.RunCompose(composePath, "restart", containerName); err != nil {
				color.Red("Error restarting container:", err)
			}
		} else {
			if err := docker.RunCompose(composePath, "restart"); err != nil {
				color.Red("Error restarting Docker Compose setup:", err)
			}
		}
	},
}

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command in the Docker container",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			return
		}

		if len(args) == 0 {
			color.Yellow("No command provided")
			return
		}

		// if the next argument is "php", "nginx" or "litespeed", use it as the service name
		// otherwise, use "php" as the default service name
		composeArgs := []string{"exec"}
		if args[0] == "php" || args[0] == "nginx" || args[0] == "litespeed" {
			composeArgs = append(composeArgs, args[0])
			args = args[1:]
		} else {
			composeArgs = append(composeArgs, "php")
		}

		composeArgs = append(composeArgs, args...)

		if err := docker.RunCompose(composePath, composeArgs...); err != nil {
			color.Red("Error executing command: %v\n", err)
		}
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs of the Docker container",
	Long:  `Show logs of Docker container(s). If no container is specified, it shows logs for all containers.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			utils.ShowNoComposeError()
			os.Exit(1)
		}

		composeArgs := []string{"logs"}
		if len(args) == 1 {
			composeArgs = append(composeArgs, args[0])
		}

		if err := docker.RunCompose(composePath, composeArgs...); err != nil {
			color.Red("Error showing logs: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(wpCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(logsCmd)
}
