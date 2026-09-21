package cmd

import (
	"errors"
	"fmt"
	"slices"

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
	composePath, err := utils.FindComposeFile(domain)
	if errors.Is(err, utils.ErrComposeNotFound) && domain == "" {
		return "", errNoSite
	}

	return composePath, err
}

var wpCmd = &cobra.Command{
	Use:   "wp [wp-cli command] [args...]",
	Short: "Run wp-cli commands",
	Long: `Run wp-cli commands in the site's PHP container.

All arguments after the first wp-cli word go to wp-cli unchanged, flags included.
Put --domain before the wp-cli command:

  fly --domain example.com wp plugin list --format=json

To pass a flag as the first argument, put -- before it:

  fly wp -- --info`,
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

// execServices are the services that fly exec accepts as its first argument.
var execServices = []string{"php", "nginx", "openlitespeed"}

// splitService splits the arguments of fly exec into a service name and a
// command. The service is empty when the first argument is not a service.
func splitService(args []string) (service string, command []string) {
	if len(args) > 0 && slices.Contains(execServices, args[0]) {
		return args[0], args[1:]
	}

	return "", args
}

var execCmd = &cobra.Command{
	Use:   "exec [service] command [args...]",
	Short: "Execute a command in the Docker container",
	Long: `Execute a command in a Docker container of the site.

If the first argument is php, nginx or openlitespeed, the command runs in that
service. Otherwise it runs in the site's PHP service (php or openlitespeed).
All arguments after the first one go to the command unchanged, flags included.
Put --domain before the command:

  fly --domain example.com exec php ls -la`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		service, command := splitService(args)
		if service == "" {
			if service, err = docker.DefaultService(composePath); err != nil {
				return err
			}
		}

		if len(command) == 0 {
			return fmt.Errorf("no command given for service %q", service)
		}

		return docker.RunCompose(composePath, append([]string{"exec", service}, command...)...)
	},
}

var (
	logsFollow bool
	logsTail   string
)

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Show logs of the Docker container",
	Long:  `Show logs of Docker container(s). If no container is specified, it shows logs for all containers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		composePath, err := siteComposePath()
		if err != nil {
			return err
		}

		composeArgs := []string{"logs"}
		if logsFollow {
			composeArgs = append(composeArgs, "--follow")
		}
		if logsTail != "" {
			composeArgs = append(composeArgs, "--tail", logsTail)
		}

		return docker.RunCompose(composePath, append(composeArgs, args...)...)
	},
}

func init() {
	// Add domain flag to rootCmd
	rootCmd.PersistentFlags().StringVar(&domain, "domain", "", "Specify domain for executing commands in a specific site")

	// Flags after the first argument belong to wp-cli or to the command.
	wpCmd.Flags().SetInterspersed(false)
	execCmd.Flags().SetInterspersed(false)

	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().StringVar(&logsTail, "tail", "", "Number of lines to show from the end of the logs")

	rootCmd.AddCommand(wpCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(logsCmd)
	requireDocker(wpCmd, startCmd, stopCmd, restartCmd, execCmd, logsCmd)
}
