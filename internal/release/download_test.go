package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func serveFile(t *testing.T, data []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fly-linux-amd64.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/fly-linux-amd64.tar.gz"
}

func TestDownloadAndInstall(t *testing.T) {
	data := archive(t, map[string]string{"fly-linux-amd64": "new"}).Bytes()
	url := serveFile(t, data)
	dir := t.TempDir()
	exe := filepath.Join(dir, "fly")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The hash is compared without regard to case.
	path, err := Download(context.Background(), url, strings.ToUpper(sum(data)), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path) }()

	if err := Install(path, exe, "fly-linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Errorf("binary = %q, want the new binary", got)
	}
}

func TestDownloadErrorsLeaveNoFile(t *testing.T) {
	data := archive(t, map[string]string{"fly-linux-amd64": "new"}).Bytes()
	url := serveFile(t, data)

	tests := []struct {
		name, url, sha256, want string
	}{
		{"wrong sha256", url, sum([]byte("other")), "the sha256 of"},
		{"sha256 not valid", url, "abc", "is not valid"},
		{"not found", strings.Replace(url, "fly-linux-amd64", "missing", 1), sum(data), "404"},
		{"plain http to a remote host", "http://github.com/flywp/server-cli/fly.tar.gz", sum(data), "must be https"},
		{"not a URL", "://", sum(data), "not valid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path, err := Download(context.Background(), tt.url, tt.sha256, dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Download() = %q, %v; want an error with %q", path, err, tt.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("Download() left %d files in the directory", len(entries))
			}
		})
	}
}

func TestDownloadLimitsTheSize(t *testing.T) {
	big := make([]byte, maxArchiveSize+10)
	url := serveFile(t, big)
	dir := t.TempDir()

	if _, err := Download(context.Background(), url, sum(big), dir); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("Download() error = %v, want a size error", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("Download() left %d files in the directory", len(entries))
	}
}

func TestDownloadRefusesARedirectToPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the download followed a redirect to plain http")
	}))
	defer plain.Close()
	// A plain-http host that is not loopback, reached through a redirect.
	remote := strings.Replace(plain.URL, "127.0.0.1", "localtest.invalid", 1)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, remote+"/fly.tar.gz", http.StatusFound)
	}))
	defer srv.Close()

	old := downloadClient.Transport
	downloadClient.Transport = srv.Client().Transport
	t.Cleanup(func() { downloadClient.Transport = old })

	_, err := Download(context.Background(), srv.URL+"/fly.tar.gz", sum([]byte("x")), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("Download() error = %v, want a refusal of the http redirect", err)
	}
}

func TestRemoveTempKeepsYoungFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".fly-download-1", ".fly-update-2", ".fly-download-new", "fly"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{".fly-download-1", ".fly-update-2"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}

	RemoveTemp(dir)

	var names []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// A young file can belong to an update that still runs.
	if strings.Join(names, ",") != ".fly-download-new,fly" {
		t.Errorf("directory holds %v, want the young download and the binary only", names)
	}
}

// testRelease serves a release with the archive and a checksum file, and
// returns its description.
func testRelease(t *testing.T, archive []byte, checksums string) *GithubRelease {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fly-linux-amd64.tar.gz":
			_, _ = w.Write(archive)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	rel := &GithubRelease{TagName: "v0.3.0"}
	for _, name := range []string{"fly-linux-amd64.tar.gz", "checksums.txt"} {
		if name == "checksums.txt" && checksums == "" {
			continue
		}
		rel.Assets = append(rel.Assets, Asset{Name: name, BrowserDownloadURL: srv.URL + "/" + name})
	}
	return rel
}

func TestSelfUpdateChecksTheArchive(t *testing.T) {
	data := archive(t, map[string]string{"fly-linux-amd64": "new"}).Bytes()
	other := sum([]byte("other"))

	tests := []struct {
		name, checksums, want string
	}{
		{"checksum agrees", sum(data) + "  fly-linux-arm64.tar.gz\n" + sum(data) + "  fly-linux-amd64.tar.gz\n", ""},
		{"binary mode mark", sum(data) + " *fly-linux-amd64.tar.gz\n", ""},
		{"checksum does not agree", other + "  fly-linux-amd64.tar.gz\n", "the sha256 of"},
		{"no line for the archive", other + "  fly-linux-arm64.tar.gz\n", "has no line for fly-linux-amd64.tar.gz"},
		{"no checksum file", "", "has no checksums.txt"},
		{"CRLF line ends", sum(data) + "  fly-linux-amd64.tar.gz\r\n", ""},
		{"upper case hex", strings.ToUpper(sum(data)) + "  fly-linux-amd64.tar.gz\n", ""},
		{"the same line two times", sum(data) + "  fly-linux-amd64.tar.gz\n" + sum(data) + "  fly-linux-amd64.tar.gz\n", ""},
		{"two different sums", sum(data) + "  fly-linux-amd64.tar.gz\n" + other + "  fly-linux-amd64.tar.gz\n", "two different sums"},
		{"a similar name", sum(data) + "  fly-linux-amd64.tar.gz.sig\n", "has no line for fly-linux-amd64.tar.gz"},
		{"empty checksum file", "\n", "has no line for fly-linux-amd64.tar.gz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "fly")
			if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}

			err := selfUpdate(context.Background(), testRelease(t, data, tt.checksums), exe, "linux", "amd64")

			got, _ := os.ReadFile(exe)
			if tt.want == "" {
				if err != nil || string(got) != "new" {
					t.Fatalf("selfUpdate() = %v, binary %q; want the new binary", err, got)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 1 {
					t.Errorf("the directory holds %d files after the update, want only the binary", len(entries))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("selfUpdate() error = %v, want %q", err, tt.want)
			}
			if string(got) != "old" {
				t.Errorf("binary = %q, want the old binary unchanged", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("the directory holds %d files, want only the binary", len(entries))
			}
		})
	}
}
