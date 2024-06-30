package utils

import (
	"os"
	"path/filepath"
)

func FindComposeFile() string {
	dir, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()

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
