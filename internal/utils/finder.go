package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrComposeNotFound means that the site has no docker-compose.yml file.
var ErrComposeNotFound = errors.New("no docker-compose.yml file found")

// hostnameLabel matches one DNS label: letters, digits and inner hyphens.
var hostnameLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidateDomain returns an error if domain is not a valid hostname. A valid
// hostname has no path separators and no "..", so it can only name a
// directory directly inside the sites directory.
func ValidateDomain(domain string) error {
	if len(domain) == 0 || len(domain) > 253 {
		return fmt.Errorf("invalid domain %q", domain)
	}

	for _, label := range strings.Split(domain, ".") {
		if !hostnameLabel.MatchString(label) {
			return fmt.Errorf("invalid domain %q", domain)
		}
	}

	return nil
}

// FindComposeFile returns the docker-compose.yml file of a site. With a
// domain, it looks in ~/<domain>. Without one, it searches from the current
// directory up to the home directory. It returns ErrComposeNotFound if the
// site has no compose file.
func FindComposeFile(domain string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	homeDir = filepath.Clean(homeDir)

	// If domain is provided, check in ~/domain/docker-compose.yml
	if domain != "" {
		if err := ValidateDomain(domain); err != nil {
			return "", err
		}

		siteDir := filepath.Join(homeDir, domain)
		if filepath.Dir(siteDir) != homeDir {
			return "", fmt.Errorf("invalid domain %q", domain)
		}

		composePath := filepath.Join(siteDir, "docker-compose.yml")
		if _, err := os.Stat(composePath); errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w for domain %q", ErrComposeNotFound, domain)
		} else if err != nil {
			return "", err
		}

		return composePath, nil
	}

	// Otherwise search from current directory up to home directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		composePath := filepath.Join(dir, "docker-compose.yml")
		if _, err := os.Stat(composePath); err == nil {
			return composePath, nil
		}

		// Stop at the home directory, or at the filesystem root when the
		// current directory is outside the home directory.
		parent := filepath.Dir(dir)
		if dir == homeDir || parent == dir {
			return "", ErrComposeNotFound
		}

		dir = parent
	}
}
