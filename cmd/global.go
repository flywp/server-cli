package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/flywp/server-cli/internal/docker"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of all services",
	Run: func(cmd *cobra.Command, args []string) {
		homeDir := "/home/fly"

		// check if .fly directory exists
		if _, err := os.Stat(homeDir + "/.fly"); os.IsNotExist(err) {
			color.Red("Root .fly directory does not exist")
		} else {
			color.Green("Root .fly directory exists")
		}

		// check if docker-compose.yml exists
		if _, err := os.Stat(homeDir + "/.fly/docker-compose.yml"); os.IsNotExist(err) {
			color.Red("Root docker-compose.yml does not exist")
		} else {
			color.Green("Root docker-compose.yml exists")
		}

		// check if .provisions directory exists
		if _, err := os.Stat(homeDir + "/.provisions"); os.IsNotExist(err) {
			color.Red(".provisions directory does not exist")
		} else {
			color.Green(".provisions directory exists")
		}

		// check if mysql directory exists
		if _, err := os.Stat(homeDir + "/.fly/database/mysql"); os.IsNotExist(err) {
			color.Red("MySQL directory does not exist")
		} else {
			color.Green("MySQL directory exists")
		}

		// check if redis directory exists
		if _, err := os.Stat(homeDir + "/.fly/database/redis"); os.IsNotExist(err) {
			color.Red("Redis directory does not exist")
		} else {
			color.Green("Redis directory exists")
		}

		// check if nginx directory exists
		if _, err := os.Stat(homeDir + "/.fly/nginx"); os.IsNotExist(err) {
			color.Red("Nginx directory does not exist")
		} else {
			color.Green("Nginx directory exists")
		}

		// check if docker is installed
		if output, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
			color.Red("Docker is not installed")
		} else {
			color.Green("Docker is installed, version: %s", strings.TrimSpace(string(output)))
		}

		// check if docker is running
		if _, err := exec.Command("docker", "version").CombinedOutput(); err != nil {
			color.Red("Docker is not running")
		} else {
			color.Green("Docker is running")
		}
	},
}

var sitesCmd = &cobra.Command{
	Use:   "sites",
	Short: "Manage all sites",
}

var sitesStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		return forEachSite("Starting", "up", "-d")
	},
}

var sitesStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		return forEachSite("Stopping", "down")
	},
}

var restartSitesCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart all sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Start the sites even if some of them failed to stop.
		stopErr := forEachSite("Stopping", "down")
		startErr := forEachSite("Starting", "up", "-d")
		return errors.Join(stopErr, startErr)
	},
}

// sitesDir holds one directory per site, each with its own docker-compose.yml.
var sitesDir = "/home/fly"

// forEachSite runs docker compose with args for every site in sitesDir.
// It continues after a failed site and returns all failures together.
func forEachSite(verb string, args ...string) error {
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		return fmt.Errorf("reading sites directory: %w", err)
	}

	var errs []error
	foundSite := false
	for _, entry := range entries {
		// Skip files and hidden directories
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		composePath := filepath.Join(sitesDir, entry.Name(), "docker-compose.yml")
		if _, err := os.Stat(composePath); err != nil {
			continue
		}

		foundSite = true
		color.Yellow("%s site in %s", verb, entry.Name())
		if err := docker.RunCompose(composePath, args...); err != nil {
			// %v, not %w: a failed child process must not hide this summary.
			// The verb tells restart's stop failures from its start failures.
			errs = append(errs, fmt.Errorf("%s %s: %v", strings.ToLower(verb), entry.Name(), err))
		}
	}

	if !foundSite {
		fmt.Println("No sites found.")
	}

	return errors.Join(errs...)
}

func init() {
	sitesCmd.AddCommand(sitesStartCmd)
	sitesCmd.AddCommand(sitesStopCmd)
	sitesCmd.AddCommand(restartSitesCmd)

	rootCmd.AddCommand(sitesCmd)
	rootCmd.AddCommand(statusCmd)
}
