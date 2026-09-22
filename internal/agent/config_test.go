package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "flyagt_0123456789abcdefghijABCDEFGHIJ"

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		EnvURL:      "https://app.flywp.com",
		EnvToken:    testToken,
		EnvServerID: "59",
		EnvStateDir: t.TempDir(),
	}
}

func getenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestConfigFromEnv(t *testing.T) {
	env := validEnv(t)
	env[EnvURL] = "https://app.flywp.com/"

	cfg, err := ConfigFromEnv(getenv(env))
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.URL.String(); got != "https://app.flywp.com" {
		t.Errorf("URL = %q, want the trailing slash removed", got)
	}
	if cfg.Token != testToken || cfg.ServerID != 59 || cfg.StateDir != env[EnvStateDir] {
		t.Errorf("ConfigFromEnv() = %+v", cfg)
	}
	if got := cfg.Offset(); got != 59*time.Second {
		t.Errorf("Offset() = %v, want 59s", got)
	}
}

func TestConfigOffsetWraps(t *testing.T) {
	if got := (Config{ServerID: 125}).Offset(); got != 5*time.Second {
		t.Errorf("Offset() = %v, want 5s (125 %% 60)", got)
	}
}

func TestConfigStateDirectoryList(t *testing.T) {
	env := validEnv(t)
	first := env[EnvStateDir]
	env[EnvStateDir] = first + ":" + t.TempDir()

	cfg, err := ConfigFromEnv(getenv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != first {
		t.Errorf("StateDir = %q, want the first directory %q", cfg.StateDir, first)
	}
}

func TestConfigFromEnvErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, key, value, want string
	}{
		{"no URL", EnvURL, "", "FLY_AGENT_URL is not set"},
		{"plain http", EnvURL, "http://app.flywp.com", "must be an https URL"},
		{"other scheme", EnvURL, "ftp://app.flywp.com", "must be an https URL"},
		{"no host", EnvURL, "https://", "not a valid URL"},
		{"no token", EnvToken, "", "FLY_AGENT_TOKEN is not set"},
		{"token with newline", EnvToken, "flyagt_abc\nX-Other: 1", "FLY_AGENT_TOKEN may contain only printable ASCII"},
		{"token with a space", EnvToken, "flyagt_abc def", "FLY_AGENT_TOKEN may contain only printable ASCII"},
		{"token that is not ASCII", EnvToken, "flyagt_é", "FLY_AGENT_TOKEN may contain only printable ASCII"},
		{"token with invalid UTF-8", EnvToken, "flyagt_\x85", "FLY_AGENT_TOKEN may contain only printable ASCII"},
		{"token with a zero-width space", EnvToken, "flyagt_\u200b", "FLY_AGENT_TOKEN may contain only printable ASCII"},
		{"URL with a query", EnvURL, "https://app.flywp.com?x=1", "must not contain a query"},
		{"URL with a fragment", EnvURL, "https://app.flywp.com#x", "must not contain a query or a fragment"},
		{"no server id", EnvServerID, "", "FLY_AGENT_SERVER_ID is not set"},
		{"negative server id", EnvServerID, "-1", "FLY_AGENT_SERVER_ID must be an integer"},
		{"text server id", EnvServerID, "abc", "FLY_AGENT_SERVER_ID must be an integer"},
		{"no state directory", EnvStateDir, "", "STATE_DIRECTORY is not set"},
		{"missing state directory", EnvStateDir, filepath.Join(t.TempDir(), "missing"), "STATE_DIRECTORY"},
		{"state directory is a file", EnvStateDir, file, "STATE_DIRECTORY is not a directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv(t)
			env[tt.key] = tt.value

			_, err := ConfigFromEnv(getenv(env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ConfigFromEnv() error = %v, want it to contain %q", err, tt.want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("error %q contains the token", err)
			}
		})
	}
}

func TestConfigFromEnvNamesEachProblem(t *testing.T) {
	_, err := ConfigFromEnv(getenv(map[string]string{}))
	if err == nil {
		t.Fatal("ConfigFromEnv() = nil, want an error")
	}
	for _, key := range []string{EnvURL, EnvToken, EnvServerID, EnvStateDir} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}

func TestConfigAcceptsHTTPSAndLoopbackHTTP(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080", "http://LOCALHOST:8080", "HTTPS://app.flywp.com"} {
		env := validEnv(t)
		env[EnvURL] = u
		if _, err := ConfigFromEnv(getenv(env)); err != nil {
			t.Errorf("ConfigFromEnv(%s) error = %v, want it accepted", u, err)
		}
	}
}

func TestConfigURLWithPasswordDoesNotShowIt(t *testing.T) {
	for _, u := range []string{"https://user:s3cret@app.flywp.com", "http://user:s3cret@example.com"} {
		env := validEnv(t)
		env[EnvURL] = u

		_, err := ConfigFromEnv(getenv(env))
		if err == nil || !strings.Contains(err.Error(), "must not contain a user or a password") {
			t.Errorf("ConfigFromEnv(%s) error = %v, want a refusal", u, err)
		}
		if err != nil && strings.Contains(err.Error(), "s3cret") {
			t.Errorf("error %q shows the password", err)
		}
	}
}
