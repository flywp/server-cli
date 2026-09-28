package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/flywp/server-cli/internal/release"
	"github.com/flywp/server-cli/internal/service"
	"github.com/flywp/server-cli/internal/version"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	yesFlag bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fly-cli",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("fly-cli version %s\n", version.Version)
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update fly-cli to the latest version",
	Long: `Update fly-cli to the latest release. When fly already runs the latest
release, the command does nothing. On a server with the monitoring agent, the
command also restarts the agent, so that the agent runs the new binary.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 {
			return errors.New("the update command must be run as root, please run 'sudo fly update'")
		}

		// Ctrl-C stops the download, and the partial download is removed.
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		update, err := release.CheckForUpdates(ctx)
		if err != nil {
			return fmt.Errorf("checking for updates: %w", err)
		}

		latest := update.Release.TagName
		switch {
		case !update.Comparable:
			fmt.Printf("This is not a release build (version %s). Latest release: %s\n", version.Version, latest)
		case !update.Available:
			fmt.Println("You are already running the latest version.")
			return restartStaleAgent(ctx)
		default:
			fmt.Printf("New version available: %s\n", latest)
		}

		if !yesFlag {
			ok, err := confirm(os.Stdin, os.Stdout, latest)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("Update cancelled.")
				return nil
			}
		}

		fmt.Println("Updating...")
		if err := release.SelfUpdate(ctx, update.Release); err != nil {
			return fmt.Errorf("updating: %w", err)
		}
		fmt.Printf("Updated to %s.\n", latest)

		return restartAgent(ctx)
	},
}

// confirm asks whether to install latest. Without a terminal nobody can
// answer, so it returns an error: a script must not read "cancelled" as done.
func confirm(in *os.File, out io.Writer, latest string) (bool, error) {
	if !isatty.IsTerminal(in.Fd()) && !isatty.IsCygwinTerminal(in.Fd()) {
		return false, errors.New("there is no terminal to confirm the update: run 'sudo fly update --yes'")
	}

	_, _ = fmt.Fprintf(out, "Do you want to install %s? (y/n): ", latest)
	var response string
	// An empty or unreadable answer cancels the update.
	_, _ = fmt.Fscanln(in, &response)

	return response == "y" || response == "Y", nil
}

// restartAgent restarts the monitoring agent, if the server has it, so that it
// runs the new binary.
func restartAgent(ctx context.Context) error {
	if !service.Installed() {
		return nil
	}

	fmt.Println("Restarting the monitoring agent...")
	if err := service.Restart(ctx); err != nil {
		return fmt.Errorf("the update is installed, but the monitoring agent did not restart: %w", err)
	}

	return nil
}

// restartStaleAgent restarts the monitoring agent when it still runs a binary
// that an earlier update replaced.
func restartStaleAgent(ctx context.Context) error {
	if !service.Installed() {
		return nil
	}

	stale, err := service.Stale(ctx)
	if err != nil {
		return fmt.Errorf("checking the monitoring agent: %w", err)
	}
	if !stale {
		return nil
	}

	fmt.Println("The monitoring agent runs an older binary.")
	return restartAgent(ctx)
}

func init() {
	updateCmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Automatically answer yes to update confirmation")
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
}
