package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "fly",
	Short: "Fly CLI for managing WordPress sites",
	Long:  `A CLI tool for managing WordPress sites using Docker and custom commands.`,
	// Execute reports errors itself, so that errors from child processes
	// are not printed twice and usage is not printed for runtime errors.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() != "update" && os.Geteuid() == 0 {
			return fmt.Errorf("you should not run this command as root")
		}

		return nil
	},
}

// Execute runs the root command and exits with the resulting status code.
func Execute() {
	os.Exit(exitCode(rootCmd.Execute(), os.Stderr))
}

// exitCode reports err on stderr and returns the exit status for it.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	// A child process (docker compose, wp-cli) has already reported its own
	// error, so pass its exit status through without printing anything.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code > 0 {
			return code
		}
		return 1
	}

	_, _ = color.New(color.FgRed).Fprintf(stderr, "Error: %v\n", err)
	return 1
}

func init() {
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w\nRun '%s --help' for usage", err, cmd.CommandPath())
	})
}
