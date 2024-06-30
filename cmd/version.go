package cmd

import (
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
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			fmt.Println("Error: The update command must be run as root.")
			fmt.Println("Please run 'sudo fly update'")
			return
		}

		latestVersion, hasUpdate, err := utils.CheckForUpdates()
		if err != nil {
			fmt.Println("Error checking for updates:", err)
			return
		}

		if !hasUpdate {
			fmt.Println("You are already running the latest version.")
			return
		}

		fmt.Printf("New version available: %s\n", latestVersion)

		if !yesFlag {
			fmt.Print("Do you want to update? (y/n): ")
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				fmt.Println("Update cancelled.")
				return
			}
		}

		fmt.Println("Updating...")
		if err := utils.SelfUpdate(); err != nil {
			fmt.Println("Error updating:", err)
		} else {
			fmt.Println("Update successful. Please restart fly cli.")
			os.Exit(0)
		}
	},
}

func init() {
	updateCmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Automatically answer yes to update confirmation")
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
}
