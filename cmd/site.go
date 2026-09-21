package cmd

import (
	"errors"
	"fmt"

	"github.com/flywp/server-cli/internal/docker"
	"github.com/flywp/server-cli/internal/utils"
	"github.com/spf13/cobra"
)

// Define domain flag as a global variable
var domain string

var errNoSite = errors.New(`no docker-compose.yml file found

You are not inside a site directory.
Please run this command from inside a site directory, e.g:
  cd ~/example.com
  fly start

Or specify the domain name:
  fly start --domain example.com`)

// siteComposePath returns the compose file of the site selected by --domain
// or by the current directory.
func siteComposePath() (string, error) {
	composePath := utils.FindComposeFile(domain)
	if composePath != "" {
		return composePath, nil
	}

	if domain != "" {
		return "", fmt.Errorf("no docker-compose.yml file found for domain %q", domain)
	}

	return "", errNoSite
}

var wpCmd = &cobra.Command{
	Use:   "wp",
	Short: "Run wp-cli commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		return docker.RunWPCLI(composePath, args)
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the site",
	Long:  "Start the Docker container for the site",
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		if err := docker.RunCompose(composePath, "up", "-d"); err != nil {
			return fmt.Errorf("starting site: %w", err)
		}
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the site",
	Long:  "Stop the Docker container for the site",
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		if err := docker.RunCompose(composePath, "down"); err != nil {
			return fmt.Errorf("stopping site: %w", err)
		}
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart [container]",
	Short: "Restart the site or a specific container",
	Long:  "Restart the Docker containers for the site. Optionally, specify a container to restart only that container.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		if err := docker.RunCompose(composePath, append([]string{"restart"}, args...)...); err != nil {
			return fmt.Errorf("restarting site: %w", err)
		}
		return nil
	},
}

var execCmd = &cobra.Command{
	Use:   "exec [service] command [args...]",
	Short: "Execute a command in the Docker container",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		// if the next argument is "php", "nginx" or "openlitespeed", use it as the service name
		// otherwise, use "php" as the default service name
		composeArgs := []string{"exec"}
		if args[0] == "php" || args[0] == "nginx" || args[0] == "openlitespeed" {
			composeArgs = append(composeArgs, args[0])
			args = args[1:]
		} else {
			composeArgs = append(composeArgs, "php")
		}

		composeArgs = append(composeArgs, args...)

		return docker.RunCompose(composePath, composeArgs...)
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs of the Docker container",
	Long:  `Show logs of Docker container(s). If no container is specified, it shows logs for all containers.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		return docker.RunCompose(composePath, append([]string{"logs"}, args...)...)
	},
}

func init() {
	// Add domain flag to rootCmd
	rootCmd.PersistentFlags().StringVar(&domain, "domain", "", "Specify domain for executing commands in a specific site")

	rootCmd.AddCommand(wpCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(logsCmd)
}
