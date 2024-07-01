package cmd

import (
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
	Run: func(cmd *cobra.Command, args []string) {
		startAllSites()
	},
}

var sitesStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all sites",
	Run: func(cmd *cobra.Command, args []string) {
		stopAllSites()
	},
}

var restartSitesCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart all sites",
	Run: func(cmd *cobra.Command, args []string) {
		stopAllSites()
		startAllSites()
	},
}

func startAllSites() {
	sitesDir := "/home/fly"
	foundSite := false

	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		color.Red("Error reading directory %s: %v\n", sitesDir, err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			path := filepath.Join(sitesDir, entry.Name())
			composePath := filepath.Join(path, "docker-compose.yml")
			if _, err := os.Stat(composePath); err == nil {
				color.Yellow("Starting site in %s\n", filepath.Base(path))
				docker.RunCompose(composePath, "up", "-d")
				foundSite = true
			}
		}
	}

	if !foundSite {
		fmt.Println("No sites found to start.")
	}
}

func stopAllSites() {
	sitesDir := "/home/fly"
	foundSite := false

	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		color.Red("Error reading directory %s: %v\n", sitesDir, err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			path := filepath.Join(sitesDir, entry.Name())
			composePath := filepath.Join(path, "docker-compose.yml")
			if _, err := os.Stat(composePath); err == nil {
				color.Yellow("Stopping site in %s\n", filepath.Base(path))
				docker.RunCompose(composePath, "down")
				foundSite = true
			}
		}
	}

	if !foundSite {
		fmt.Println("No sites found to stop.")
	}
}

func init() {
	sitesCmd.AddCommand(sitesStartCmd)
	sitesCmd.AddCommand(sitesStopCmd)
	sitesCmd.AddCommand(restartSitesCmd)

	rootCmd.AddCommand(sitesCmd)
	rootCmd.AddCommand(statusCmd)
}
