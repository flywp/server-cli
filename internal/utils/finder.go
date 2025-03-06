package utils

import (
	"os"
	"path/filepath"
)

func FindComposeFile(domain string) string {
	homeDir, _ := os.UserHomeDir()

	// If domain is provided, check in ~/domain/docker-compose.yml
	if domain != "" {
		composePath := filepath.Join(homeDir, domain, "docker-compose.yml")
		if _, err := os.Stat(composePath); err == nil {
			return composePath
		}
		return ""
	}

	// Otherwise search from current directory up to home directory
	dir, _ := os.Getwd()

	for {
		composePath := filepath.Join(dir, "docker-compose.yml")

		if _, err := os.Stat(composePath); err == nil {
			return composePath
		}

		if dir == homeDir {
			return ""
		}

		dir = filepath.Dir(dir)
	}
}
