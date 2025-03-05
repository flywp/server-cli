package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/flywp/server-cli/internal/version"
)

const GithubAPI = "https://api.github.com/repos/flywp/server-cli/releases/latest"

type GithubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func CheckForUpdates() (string, bool, error) {
	resp, err := http.Get(GithubAPI)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}

	var release GithubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return "", false, err
	}

	return release.TagName, release.TagName > version.Version, nil
}

func SelfUpdate() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("the update command must be run as root")
	}

	release, err := getLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest release: %w", err)
	}

	assetURL := getAssetURL(release)
	if assetURL == "" {
		return fmt.Errorf("no suitable binary found for this system (OS: %s, ARCH: %s)", runtime.GOOS, runtime.GOARCH)
	}

	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "fly-cli-update")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download the archive
	resp, err := http.Get(assetURL)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download update: HTTP %d", resp.StatusCode)
	}

	// Create the archive file
	archivePath := filepath.Join(tmpDir, "update.tar.gz")
	out, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return fmt.Errorf("failed to write archive file: %w", err)
	}

	// Extract the archive
	binaryName := fmt.Sprintf("fly-%s-%s", runtime.GOOS, runtime.GOARCH)
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	// Get the current executable path
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Make the new binary executable
	extractedBinary := filepath.Join(tmpDir, binaryName)
	if err := os.Chmod(extractedBinary, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	// Rename the temporary file to the executable name
	if err := os.Rename(extractedBinary, exe); err != nil {
		return fmt.Errorf("failed to replace old binary: %w", err)
	}

	return nil
}

func getLatestRelease() (*GithubRelease, error) {
	resp, err := http.Get(GithubAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var release GithubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, err
	}

	return &release, nil
}

func getAssetURL(release *GithubRelease) string {
	arch := runtime.GOARCH
	if runtime.GOOS != "linux" {
		return ""
	}

	expectedName := fmt.Sprintf("fly-linux-%s.tar.gz", arch)
	for _, asset := range release.Assets {
		if asset.Name == expectedName {
			return asset.BrowserDownloadURL
		}
	}

	return ""
}
