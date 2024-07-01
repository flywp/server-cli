package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "fly",
	Short: "Fly CLI for managing WordPress sites",
	Long:  `A CLI tool for managing WordPress sites using Docker and custom commands.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() != "update" && os.Geteuid() == 0 {
			return fmt.Errorf("you should not run this command as root")
		}

		return nil
	},
}

// Execute the root command.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.server-cli.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	// rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
