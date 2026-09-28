// Package release finds, downloads, checks and installs fly releases. "fly
// update" and the agent command agent.update use it.
package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/flywp/server-cli/internal/version"
	"golang.org/x/mod/semver"
)

// GithubAPI is the GitHub API URL of the latest release.
var GithubAPI = "https://api.github.com/repos/flywp/server-cli/releases/latest"

// httpClient limits each request, including the download of the binary.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// maxBinarySize limits the size of the binary in a release archive.
const maxBinarySize = 200 << 20

// GithubRelease is a release in the GitHub API.
type GithubRelease struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is a file of a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Update compares the latest release with the running version.
type Update struct {
	Release *GithubRelease
	// Available is true when the release is newer than the running version.
	Available bool
	// Comparable is false when the running version is not built from a
	// release tag, for example "dev".
	Comparable bool
}

// CheckForUpdates gets the latest release from GitHub and compares it with
// the running version.
func CheckForUpdates(ctx context.Context) (*Update, error) {
	release, err := LatestRelease(ctx)
	if err != nil {
		return nil, err
	}

	available, comparable := isNewer(release.TagName, version.Version)
	return &Update{Release: release, Available: available, Comparable: comparable}, nil
}

// describeSuffix matches what git describe adds after a tag: the number of
// commits since the tag, the commit hash and "-dirty" for local changes.
var describeSuffix = regexp.MustCompile(`(-\d+-g[0-9a-f]+)?(-dirty)?$`)

// isNewer reports whether the release version latest is newer than current.
// comparable is false when current is not built from a release tag.
func isNewer(latest, current string) (newer, comparable bool) {
	base := describeSuffix.ReplaceAllString(current, "")
	if !semver.IsValid(base) {
		return false, false
	}

	return semver.Compare(latest, base) > 0, true
}

// LatestRelease returns the latest release from GitHub.
func LatestRelease(ctx context.Context) (*GithubRelease, error) {
	resp, err := get(ctx, GithubAPI)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("reading release information: %w", err)
	}

	if !semver.IsValid(release.TagName) {
		return nil, fmt.Errorf("latest release has an invalid version %q", release.TagName)
	}

	return &release, nil
}

// get sends a GET request and returns the response if its status is 200 OK.
func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fly-cli/"+version.Version)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		limited := resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests
		if limited && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("the GitHub API rate limit is exceeded, try again later")
		}
		return nil, fmt.Errorf("unexpected response from %s: %s", url, resp.Status)
	}

	return resp, nil
}

// ChecksumsAsset is the checksum file of each release: one line for each
// archive, "<sha256>  <file name>" (the output of sha256sum).
const ChecksumsAsset = "checksums.txt"

// selfUpdateTimeout limits the download of an update.
const selfUpdateTimeout = 10 * time.Minute

// SelfUpdate replaces the running binary with the binary from release. It
// installs the archive only when its sha256 agrees with the checksum file of
// the release.
func SelfUpdate(ctx context.Context, release *GithubRelease) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding the current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	return selfUpdate(ctx, release, exe, runtime.GOOS, runtime.GOARCH)
}

func selfUpdate(ctx context.Context, release *GithubRelease, exe, goos, goarch string) error {
	ctx, cancel := context.WithTimeout(ctx, selfUpdateTimeout)
	defer cancel()

	archiveURL := assetURL(release, goos, goarch)
	if archiveURL == "" {
		return fmt.Errorf("no suitable binary found for this system (OS: %s, ARCH: %s)", goos, goarch)
	}

	name := BinaryName(goos, goarch)
	sum, err := checksum(ctx, release, name+".tar.gz")
	if err != nil {
		return err
	}

	archive, err := Download(ctx, archiveURL, sum, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive) }()

	return Install(archive, exe, name)
}

// checksum returns the sha256 of the release file name from the checksum
// file of the release.
func checksum(ctx context.Context, release *GithubRelease, name string) (string, error) {
	url := asset(release, ChecksumsAsset)
	if url == "" {
		return "", fmt.Errorf("release %s has no %s, so its download cannot be checked", release.TagName, ChecksumsAsset)
	}

	resp, err := get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", ChecksumsAsset, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", ChecksumsAsset, err)
	}

	// install.sh reads the file with the same rules. Two different sums for
	// one file make the file not valid: it is not clear which one is correct.
	var sum string
	for line := range strings.Lines(string(data)) {
		// sha256sum marks a file that it read in binary mode with "*".
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if sum != "" && !strings.EqualFold(sum, fields[0]) {
			return "", fmt.Errorf("%s of release %s has two different sums for %s", ChecksumsAsset, release.TagName, name)
		}
		sum = fields[0]
	}
	if sum == "" {
		return "", fmt.Errorf("%s of release %s has no line for %s", ChecksumsAsset, release.TagName, name)
	}

	return sum, nil
}

// BinaryName is the name of the binary in a release archive. Releases must
// keep this name: installed versions of fly look for it.
func BinaryName(goos, goarch string) string {
	return fmt.Sprintf("fly-%s-%s", goos, goarch)
}

// assetURL returns the download URL of the release archive for goos and
// goarch, or "" if the release has none.
func assetURL(release *GithubRelease, goos, goarch string) string {
	if goos != "linux" {
		return ""
	}

	return asset(release, BinaryName(goos, goarch)+".tar.gz")
}

// asset returns the download URL of the release file name, or "".
func asset(release *GithubRelease, name string) string {
	for _, a := range release.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}

	return ""
}

// replaceBinary extracts the file name from the tar.gz archive and puts it in
// place of exe. The new binary is written to a temporary file in the same
// directory and then renamed, so exe is never incomplete and the rename
// does not cross filesystems.
func replaceBinary(exe string, archive io.Reader, name string) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive does not contain %s", name)
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg || path.Clean(hdr.Name) != name {
			continue
		}
		if hdr.Size > maxBinarySize {
			return fmt.Errorf("%s in archive is too large (%d bytes)", name, hdr.Size)
		}

		return writeBinary(exe, tr)
	}
}

// writeBinary writes r to a temporary file next to exe and renames it to exe.
func writeBinary(exe string, r io.Reader) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".fly-update-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing new binary: %w", err)
	}
	// Change the open file, never the path: the directory can belong to an
	// other user (the agent's ~fly/.fly/bin), who could put a link to a
	// different file in place of the temporary file.
	if err = tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("making binary executable: %w", err)
	}
	if err = keepOwner(tmp, exe); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("keeping the owner of the binary: %w", err)
	}
	// Put the binary on the disk before the rename: after a power loss, a
	// renamed but empty binary would not start.
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing new binary: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("writing new binary: %w", err)
	}
	if err = os.Rename(tmp.Name(), exe); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	// Sync the directory too, so that the rename survives a power loss.
	if d, err := os.Open(filepath.Dir(exe)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	return nil
}

// keepOwner gives the open file f the owner of exe. "sudo fly update" then
// keeps the binary of the agent with the server user, not with root. Only
// root can give a file to a different user; for other users the owner is
// already correct. It changes the open file, not a path: a path in a
// directory of an other user can be replaced by a link to a root file.
func keepOwner(f *os.File, exe string) error {
	info, err := os.Stat(exe)
	if err != nil {
		return nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}

	return f.Chown(int(st.Uid), int(st.Gid))
}
