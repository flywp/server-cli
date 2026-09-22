package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxArchiveSize limits the size of a downloaded release archive.
const maxArchiveSize = 200 << 20

// downloadClient has no timeout of its own: the context of the caller limits
// the download, because a slow network can need some minutes. It follows a
// redirect (GitHub sends each download to its file host) only to https.
var downloadClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return checkURL(req.URL.String())
	},
}

// tempMaxAge is the age after which RemoveTemp removes a temporary file. A
// younger file can belong to an update that still runs.
const tempMaxAge = time.Hour

// RemoveTemp removes the temporary files that an update left in dir after a
// crash, if they are older than one hour.
func RemoveTemp(dir string) {
	for _, pattern := range []string{".fly-download-*", ".fly-update-*"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, m := range matches {
			if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > tempMaxAge {
				_ = os.Remove(m)
			}
		}
	}
}

// Download fetches the release archive at rawURL into a temporary file in dir,
// and checks that its sha256 is wantSHA256 (hex). It returns the path of the
// file; the caller removes it. On any error, no file is left.
//
// The URL must be https. Plain http is accepted only for a loopback host,
// for tests and local development.
func Download(ctx context.Context, rawURL, wantSHA256, dir string) (path string, err error) {
	if err := checkURL(rawURL); err != nil {
		return "", err
	}
	want := strings.ToLower(strings.TrimSpace(wantSHA256))
	if len(want) != sha256.Size*2 {
		return "", fmt.Errorf("the sha256 %q is not valid", wantSHA256)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", rawURL, resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".fly-download-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxArchiveSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", rawURL, err)
	}
	if n > maxArchiveSize {
		return "", fmt.Errorf("the archive at %s is larger than %d bytes", rawURL, maxArchiveSize)
	}

	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return "", fmt.Errorf("the sha256 of %s is %s, not %s", rawURL, got, want)
	}

	return tmp.Name(), nil
}

// Install extracts the binary name from the archive at archivePath and puts it
// in place of exe. exe is never incomplete: see replaceBinary.
func Install(archivePath, exe, name string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return replaceBinary(exe, f, name)
}

func checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("the download URL %q is not valid", rawURL)
	}

	switch host := u.Hostname(); {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback()):
		return nil
	}

	return fmt.Errorf("the download URL must be https, not %q", rawURL)
}
