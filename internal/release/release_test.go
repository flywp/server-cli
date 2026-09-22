package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current       string
		newer, wantComparable bool
	}{
		{latest: "v0.1.10", current: "v0.1.9", newer: true, wantComparable: true},
		{latest: "v0.1.9", current: "v0.1.10", newer: false, wantComparable: true},
		{latest: "v0.1.1", current: "v0.1.1", newer: false, wantComparable: true},
		{latest: "v0.2.0", current: "v0.1.1", newer: true, wantComparable: true},
		{latest: "v1.0.0", current: "v0.10.0", newer: true, wantComparable: true},
		// git describe versions: built after the tag, so the tag is not newer.
		{latest: "v0.1.1", current: "v0.1.1-2-g3994ef6", newer: false, wantComparable: true},
		{latest: "v0.1.2", current: "v0.1.1-2-g3994ef6", newer: true, wantComparable: true},
		{latest: "v0.1.1", current: "v0.1.1-2-g3994ef6-dirty", newer: false, wantComparable: true},
		{latest: "v0.1.1", current: "v0.1.1-dirty", newer: false, wantComparable: true},
		{latest: "v0.2.0", current: "v0.2.0-rc.1", newer: true, wantComparable: true},
		// Not built from a release tag: cannot be compared.
		{latest: "v0.2.0", current: "dev", newer: false, wantComparable: false},
		{latest: "v0.2.0", current: "3994ef6", newer: false, wantComparable: false},
	}

	for _, tt := range tests {
		newer, comparable := isNewer(tt.latest, tt.current)
		if newer != tt.newer || comparable != tt.wantComparable {
			t.Errorf("isNewer(%q, %q) = %v, %v, want %v, %v", tt.latest, tt.current, newer, comparable, tt.newer, tt.wantComparable)
		}
	}
}

// serveAPI points GithubAPI at a test server that runs handler.
func serveAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	old := GithubAPI
	GithubAPI = srv.URL
	t.Cleanup(func() { GithubAPI = old })
}

func TestLatestRelease(t *testing.T) {
	var userAgent string
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","assets":[{"name":"fly-linux-amd64.tar.gz","browser_download_url":"https://example.com/a"}]}`))
	})

	release, err := LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease() error = %v", err)
	}
	if release.TagName != "v0.2.0" || len(release.Assets) != 1 {
		t.Errorf("LatestRelease() = %+v, want v0.2.0 with one asset", release)
	}
	if !strings.HasPrefix(userAgent, "fly-cli/") {
		t.Errorf("User-Agent = %q, want fly-cli/<version>", userAgent)
	}
}

func TestLatestReleaseErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "rate limit",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			},
			want: "rate limit",
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: "500",
		},
		{
			name: "no tag",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			},
			want: "invalid version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serveAPI(t, tt.handler)

			// An error, never an empty "latest version".
			if _, err := LatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LatestRelease() error = %v, want an error that contains %q", err, tt.want)
			}
		})
	}
}

func TestAssetURL(t *testing.T) {
	release := &GithubRelease{TagName: "v0.2.0"}
	for _, name := range []string{"fly-linux-amd64.tar.gz", "fly-linux-arm64.tar.gz"} {
		release.Assets = append(release.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{Name: name, BrowserDownloadURL: "https://example.com/" + name})
	}

	tests := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "https://example.com/fly-linux-amd64.tar.gz"},
		{"linux", "arm64", "https://example.com/fly-linux-arm64.tar.gz"},
		{"linux", "386", ""},
		{"darwin", "arm64", ""},
	}
	for _, tt := range tests {
		if got := assetURL(release, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("assetURL(%s/%s) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

// archive returns a tar.gz archive that contains files.
func archive(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return &buf
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fly")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := replaceBinary(exe, archive(t, map[string]string{"fly-linux-amd64": "new"}), "fly-linux-amd64")
	if err != nil {
		t.Fatalf("replaceBinary() error = %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "new" {
		t.Errorf("binary = %q, %v, want %q", got, err, "new")
	}
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode = %v, %v, want 0755", info.Mode().Perm(), err)
	}
	assertOnlyFile(t, dir, "fly")
}

func TestReplaceBinaryKeepsOldBinaryOnError(t *testing.T) {
	tests := []struct {
		name    string
		archive *bytes.Buffer
	}{
		{name: "binary not in archive", archive: archive(t, map[string]string{"README.md": "text"})},
		{name: "not an archive", archive: bytes.NewBufferString("<html>Not Found</html>")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "fly")
			if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := replaceBinary(exe, tt.archive, "fly-linux-amd64"); err == nil {
				t.Fatal("replaceBinary() = nil, want an error")
			}

			if got, _ := os.ReadFile(exe); string(got) != "old" {
				t.Errorf("binary = %q, want the old binary unchanged", got)
			}
			assertOnlyFile(t, dir, "fly")
		})
	}
}

// assertOnlyFile fails the test if dir contains anything other than name,
// for example a temporary file that was not removed.
func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory contains %q, want only %q", names, name)
	}
}

func TestReplaceBinaryKeepsTheOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root can give a file to a different user")
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "fly")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The binary of the agent belongs to the server user, for example 1000.
	if err := os.Chown(exe, 1000, 1000); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(exe, archive(t, map[string]string{"fly-linux-amd64": "new"}), "fly-linux-amd64"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if st := info.Sys().(*syscall.Stat_t); st.Uid != 1000 || st.Gid != 1000 {
		t.Errorf("owner = %d:%d, want 1000:1000", st.Uid, st.Gid)
	}
}

// swapReader is an archive that, halfway, puts a link to victim in place of
// the temporary file in dir, like a hostile owner of dir.
type swapReader struct {
	t       *testing.T
	r       io.Reader
	dir     string
	victim  string
	swapped bool
}

func (s *swapReader) Read(p []byte) (int, error) {
	if !s.swapped {
		s.swapped = true
		matches, _ := filepath.Glob(filepath.Join(s.dir, ".fly-update-*"))
		for _, m := range matches {
			if err := os.Rename(m, m+".moved"); err != nil {
				s.t.Fatal(err)
			}
			if err := os.Symlink(s.victim, m); err != nil {
				s.t.Fatal(err)
			}
		}
	}
	return s.r.Read(p)
}

func TestReplaceBinaryDoesNotFollowALinkToAnOtherFile(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the attack needs root to write the binary")
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "fly")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(exe, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	// A root file, for example /etc/shadow.
	victim := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(victim, []byte("secret"), 0o640); err != nil {
		t.Fatal(err)
	}

	gz := &bytes.Buffer{}
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "fly-linux-amd64", Mode: 0o755, Size: 3, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("new"))
	_ = tw.Close()
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(gz.Bytes())
	_ = zw.Close()

	tr := tar.NewReader(func() io.Reader { r, _ := gzip.NewReader(&zipped); return r }())
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	_ = writeBinary(exe, &swapReader{t: t, r: tr, dir: dir, victim: victim})

	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0o640 || st.Uid != 0 {
		t.Errorf("victim = %v owned by %d, want 0640 owned by root: root followed the link", info.Mode().Perm(), st.Uid)
	}
}
