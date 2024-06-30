package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
			fmt.Println("❌ Root .fly directory does not exist")
		} else {
			fmt.Println("✅ Root .fly directory exists")
		}

		// check if docker-compose.yml exists
		if _, err := os.Stat(homeDir + "/.fly/docker-compose.yml"); os.IsNotExist(err) {
			fmt.Println("❌ Root docker-compose.yml does not exist")
		} else {
			fmt.Println("✅ Root docker-compose.yml exists")
		}

		// check if .provisions directory exists
		if _, err := os.Stat(homeDir + "/.provisions"); os.IsNotExist(err) {
			fmt.Println("❌ .provisions directory does not exist")
		} else {
			fmt.Println("✅ .provisions directory exists")
		}

		// check if mysql directory exists
		if _, err := os.Stat(homeDir + "/.fly/database/mysql"); os.IsNotExist(err) {
			fmt.Println("❌ MySQL directory does not exist")
		} else {
			fmt.Println("✅ MySQL directory exists")
		}

		// check if redis directory exists
		if _, err := os.Stat(homeDir + "/.fly/database/redis"); os.IsNotExist(err) {
			fmt.Println("❌ Redis directory does not exist")
		} else {
			fmt.Println("✅ Redis directory exists")
		}

		// check if nginx directory exists
		if _, err := os.Stat(homeDir + "/.fly/nginx"); os.IsNotExist(err) {
			fmt.Println("❌ Nginx directory does not exist")
		} else {
			fmt.Println("✅ Nginx directory exists")
		}

		// check if docker is installed
		if output, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
			fmt.Println("❌ Docker is not installed")
		} else {
			fmt.Println("✅ Docker is installed, version:", strings.TrimSpace(string(output)))
		}

		// check if docker is running
		if _, err := exec.Command("docker", "version").CombinedOutput(); err != nil {
			fmt.Println("❌ Docker is not running")
		} else {
			fmt.Println("✅ Docker is running")
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

	filepath.Walk(sitesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}

		if info.IsDir() && filepath.Base(path) != "fly" {
			composePath := filepath.Join(path, "docker-compose.yml")
			if _, err := os.Stat(composePath); err == nil {
				fmt.Printf("Starting site in %s\n", path)
				docker.RunCompose(composePath, "up", "-d")
				foundSite = true
			}
		}

		return nil
	})

	if !foundSite {
		fmt.Println("No sites found to start.")
	}
}

func stopAllSites() {
	sitesDir := "/home/fly"
	foundSite := false
	filepath.Walk(sitesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}

		if info.IsDir() && filepath.Base(path) != "fly" {
			composePath := filepath.Join(path, "docker-compose.yml")
			if _, err := os.Stat(composePath); err == nil {
				fmt.Printf("Stopping site in %s\n", path)
				docker.RunCompose(composePath, "down")
				foundSite = true
			}
		}
		return nil
	})

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
