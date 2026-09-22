package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/flywp/server-cli/internal/utils"
	"github.com/flywp/server-cli/internal/version"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 {
			return errors.New("the update command must be run as root, please run 'sudo fly update'")
		}

		update, err := utils.CheckForUpdates(cmd.Context())
		if err != nil {
			return fmt.Errorf("checking for updates: %w", err)
		}

		latest := update.Release.TagName
		switch {
		case !update.Comparable:
			fmt.Printf("This is not a release build (version %s). Latest release: %s\n", version.Version, latest)
		case !update.Available:
			fmt.Println("You are already running the latest version.")
			return nil
		default:
			fmt.Printf("New version available: %s\n", latest)
		}

		if !yesFlag {
			fmt.Printf("Do you want to install %s? (y/n): ", latest)
			var response string
			// An empty or unreadable answer cancels the update.
			_, _ = fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				fmt.Println("Update cancelled.")
				return nil
			}
		}

		fmt.Println("Updating...")
		if err := utils.SelfUpdate(cmd.Context(), update.Release); err != nil {
			return fmt.Errorf("updating: %w", err)
		}

		fmt.Printf("Updated to %s.\n", latest)
		return nil
	},
}

func init() {
	updateCmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Automatically answer yes to update confirmation")
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
}
