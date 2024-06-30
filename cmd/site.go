package cmd

import (
	"fmt"
	"os"

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
			fmt.Println("No docker-compose.yml file found")
			return
		}

		if err := docker.RunWPCLI(composePath, args); err != nil {
			fmt.Println("Error running wp-cli:", err)
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
			fmt.Println("No docker-compose.yml file found")
			return
		}

		if err := docker.RunCompose(composePath, "up", "-d"); err != nil {
			fmt.Println("Error starting container:", err)
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
			fmt.Println("No docker-compose.yml file found")
			return
		}

		if err := docker.RunCompose(composePath, "down"); err != nil {
			fmt.Println("Error stopping container:", err)
		}
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the site",
	Long:  "Restart the Docker container for the site",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			fmt.Println("No docker-compose.yml file found")
			return
		}

		if err := docker.RunCompose(composePath, "restart"); err != nil {
			fmt.Println("Error restarting container:", err)
		}
	},
}

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command in the Docker container",
	Run: func(cmd *cobra.Command, args []string) {
		composePath := utils.FindComposeFile()
		if composePath == "" {
			fmt.Println("No docker-compose.yml file found")
			return
		}

		if len(args) == 0 {
			fmt.Println("No command provided")
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
			fmt.Printf("Error executing command: %v\n", err)
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
			cmd.PrintErrln("No docker-compose.yml file found")
			os.Exit(1)
		}

		composeArgs := []string{"logs"}
		if len(args) == 1 {
			composeArgs = append(composeArgs, args[0])
		}

		if err := docker.RunCompose(composePath, composeArgs...); err != nil {
			cmd.PrintErrf("Error showing logs: %v\n", err)
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
