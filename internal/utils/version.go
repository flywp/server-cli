package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
		return err
	}

	// print debug info of release
	fmt.Printf("Release: %s\n", release.TagName)

	assetURL := getAssetURL(release)
	if assetURL == "" {
		return fmt.Errorf("no suitable binary found for this system")
	}

	// print debug info of asset
	fmt.Printf("Asset URL: %s\n", assetURL)

	// Download the new binary
	resp, err := http.Get(assetURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "fly-cli-update")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	// Write the body to file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		return err
	}

	// Close the file
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// Make it executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return err
	}

	// Get the current executable path
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	// Rename the temporary file to the executable name
	return os.Rename(tmpFile.Name(), exe)
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
	os := runtime.GOOS

	for _, asset := range release.Assets {
		if filepath.Ext(asset.Name) == ".tar.gz" &&
			((os == "linux" && arch == "amd64" && asset.Name == "fly-linux-amd64.tar.gz") ||
				(os == "linux" && arch == "arm64" && asset.Name == "fly-linux-arm64.tar.gz")) {
			return asset.BrowserDownloadURL
		}
	}

	return ""
}
