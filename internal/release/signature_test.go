package release

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var signTime = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func testKey(t *testing.T) (ed25519.PrivateKey, map[string]ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, map[string]ed25519.PublicKey{KeyID(pub): pub}
}

func TestSignThenVerify(t *testing.T) {
	priv, keys := testKey(t)
	checksums := []byte("abc  fly-linux-amd64.tar.gz\n")

	sigFile := Sign(priv, "v0.2.1", signTime, checksums)

	sig, err := Verify(sigFile, checksums, "v0.2.1", keys, signTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if sig.Tag != "v0.2.1" || !sig.SignedAt.Equal(signTime) {
		t.Errorf("Verify() = %+v, want tag v0.2.1 signed at %v", sig, signTime)
	}
	if !strings.HasPrefix(string(sigFile), "fly-release-signature-v1\nkey ") {
		t.Errorf("signature file = %q, want the v1 format", sigFile)
	}
}

func TestVerifyRefuses(t *testing.T) {
	priv, keys := testKey(t)
	otherPriv, _ := testKey(t)
	checksums := []byte("abc  fly-linux-amd64.tar.gz\n")
	good := string(Sign(priv, "v0.2.1", signTime, checksums))
	now := signTime.Add(time.Hour)

	tests := []struct {
		name      string
		sigFile   string
		checksums string
		tag       string
		now       time.Time
		want      string
	}{
		{"an unknown key", string(Sign(otherPriv, "v0.2.1", signTime, checksums)), string(checksums), "v0.2.1", now, "does not trust"},
		{"an other tag", good, string(checksums), "v0.2.2", now, "for the release v0.2.1, not v0.2.2"},
		{"a changed checksum file", good, "def  fly-linux-amd64.tar.gz\n", "v0.2.1", now, "is not correct"},
		{"a changed signature time", strings.Replace(good, "10:00:00Z", "09:00:00Z", 1), string(checksums), "v0.2.1", now, "is not correct"},
		{"a time in the future", good, string(checksums), "v0.2.1", signTime.Add(-time.Hour), "in the future"},
		{"reordered lines", reorder(good), string(checksums), "v0.2.1", now, "format"},
		{"an extra line", good + "extra\n", string(checksums), "v0.2.1", now, "format"},
		{"no end of line", strings.TrimSuffix(good, "\n"), string(checksums), "v0.2.1", now, "format"},
		{"an other format", strings.Replace(good, "-v1", "-v2", 1), string(checksums), "v0.2.1", now, "format"},
		{"a short signature", good[:strings.Index(good, "sig ")] + "sig AAAA\n", string(checksums), "v0.2.1", now, "not a valid ed25519"},
		{"an empty file", "", string(checksums), "v0.2.1", now, "format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Verify([]byte(tt.sigFile), []byte(tt.checksums), tt.tag, keys, tt.now)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Verify() error = %v, want an error that contains %q", err, tt.want)
			}
		})
	}
}

// reorder swaps the key and the tag lines of a signature file.
func reorder(sigFile string) string {
	lines := strings.Split(sigFile, "\n")
	lines[1], lines[2] = lines[2], lines[1]
	return strings.Join(lines, "\n")
}

func TestVerifyWithoutKeys(t *testing.T) {
	priv, _ := testKey(t)
	checksums := []byte("abc  fly-linux-amd64.tar.gz\n")

	// A binary without a trusted key installs no release by itself.
	if _, err := Verify(Sign(priv, "v0.2.1", signTime, checksums), checksums, "v0.2.1", nil, signTime); err == nil {
		t.Error("Verify() with no keys = nil, want an error")
	}
}

func TestParseKeys(t *testing.T) {
	pub1, _, _ := ed25519.GenerateKey(nil)
	pub2, _, _ := ed25519.GenerateKey(nil)

	keys, err := ParseKeys(KeyLine(pub1) + ", " + KeyLine(pub2))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || !keys[KeyID(pub1)].Equal(pub1) {
		t.Errorf("ParseKeys() = %v, want both keys by id", keys)
	}

	if keys, err := ParseKeys(""); err != nil || len(keys) != 0 {
		t.Errorf("ParseKeys(\"\") = %v, %v, want no keys", keys, err)
	}

	for _, bad := range []string{
		"no-colon",
		KeyID(pub1) + ":not-base64!",
		KeyID(pub1) + ":AAAA",
		KeyID(pub2) + ":" + strings.SplitN(KeyLine(pub1), ":", 2)[1],
	} {
		if _, err := ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q) = nil error, want an error", bad)
		}
	}
}

func TestTrustedKeysParse(t *testing.T) {
	// The built-in list must always parse: a bad list would stop every
	// update by itself without a clear reason.
	if _, err := TrustedKeys(); err != nil {
		t.Fatalf("TrustedKeys() error = %v", err)
	}
}

// signedRelease serves a release with an archive, a checksum file and, when
// sigFile is not nil, a signature file.
func signedRelease(t *testing.T, checksums string, sigFile []byte) *GithubRelease {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksums))
		case "/checksums.txt.sig":
			_, _ = w.Write(sigFile)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	rel := &GithubRelease{TagName: "v0.2.1"}
	names := []string{"fly-linux-amd64.tar.gz", "checksums.txt"}
	if sigFile != nil {
		names = append(names, "checksums.txt.sig")
	}
	for _, name := range names {
		rel.Assets = append(rel.Assets, Asset{Name: name, BrowserDownloadURL: srv.URL + "/" + name})
	}
	return rel
}

func TestSignedChecksum(t *testing.T) {
	priv, keys := testKey(t)
	archiveSum := sum([]byte("archive"))
	checksums := archiveSum + "  fly-linux-amd64.tar.gz\n"
	rel := signedRelease(t, checksums, Sign(priv, "v0.2.1", signTime, []byte(checksums)))

	got, err := SignedChecksum(context.Background(), rel, "linux", "amd64", keys, signTime)
	if err != nil {
		t.Fatalf("SignedChecksum() error = %v", err)
	}
	if got.SHA256 != archiveSum || !got.SignedAt.Equal(signTime) || !strings.HasSuffix(got.URL, "/fly-linux-amd64.tar.gz") {
		t.Errorf("SignedChecksum() = %+v, want the signed sum, time and archive URL", got)
	}
}

func TestSignedChecksumRefuses(t *testing.T) {
	priv, keys := testKey(t)
	checksums := sum([]byte("archive")) + "  fly-linux-amd64.tar.gz\n"

	t.Run("no signature", func(t *testing.T) {
		rel := signedRelease(t, checksums, nil)
		if _, err := SignedChecksum(context.Background(), rel, "linux", "amd64", keys, signTime); !errors.Is(err, ErrNotSigned) {
			t.Errorf("SignedChecksum() error = %v, want ErrNotSigned", err)
		}
	})

	t.Run("a signature of other checksums", func(t *testing.T) {
		rel := signedRelease(t, checksums, Sign(priv, "v0.2.1", signTime, []byte("other\n")))
		if _, err := SignedChecksum(context.Background(), rel, "linux", "amd64", keys, signTime); err == nil || !strings.Contains(err.Error(), "is not correct") {
			t.Errorf("SignedChecksum() error = %v, want a signature error", err)
		}
	})

	t.Run("no line for this arch", func(t *testing.T) {
		rel := signedRelease(t, checksums, Sign(priv, "v0.2.1", signTime, []byte(checksums)))
		rel.Assets = append(rel.Assets, Asset{Name: "fly-linux-arm64.tar.gz", BrowserDownloadURL: rel.Assets[0].BrowserDownloadURL})
		if _, err := SignedChecksum(context.Background(), rel, "linux", "arm64", keys, signTime); err == nil || !strings.Contains(err.Error(), "has no line for fly-linux-arm64.tar.gz") {
			t.Errorf("SignedChecksum() error = %v, want no line for arm64", err)
		}
	})

	t.Run("no archive for this system", func(t *testing.T) {
		rel := signedRelease(t, checksums, Sign(priv, "v0.2.1", signTime, []byte(checksums)))
		if _, err := SignedChecksum(context.Background(), rel, "darwin", "arm64", keys, signTime); err == nil || !strings.Contains(err.Error(), "has no binary") {
			t.Errorf("SignedChecksum() error = %v, want no binary for darwin", err)
		}
	})
}
