// Package agent is the FlyWP monitoring agent: the long-running mode of fly
// that "fly agent run" starts. It follows the FlyWP monitoring agent
// contract v0.3.0.
package agent

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The environment keys that the FlyWP installer writes to /etc/fly/agent.env,
// and the key that systemd sets for StateDirectory=. EnvAutoUpdate is
// optional: a person adds it to stop the updates by release on one server.
const (
	EnvURL        = "FLY_AGENT_URL"
	EnvToken      = "FLY_AGENT_TOKEN"
	EnvServerID   = "FLY_AGENT_SERVER_ID"
	EnvStateDir   = "STATE_DIRECTORY"
	EnvAutoUpdate = "FLY_AGENT_AUTO_UPDATE"
)

// Config is the configuration of the agent.
type Config struct {
	// URL is the base URL of the control plane. The agent adds the paths
	// of the contract to it.
	URL *url.URL
	// Token is the bearer token of the agent. It is opaque: the agent sends
	// it and never parses it. Never log it.
	Token string
	// ServerID sets the second of the minute at which the agent works. The
	// agent never sends it.
	ServerID int64
	// StateDir keeps the files that must survive a restart.
	StateDir string
	// AutoUpdate lets the agent install a new signed release by itself, one
	// time each day. The command agent.update works in both cases.
	AutoUpdate bool
	// Warnings are the problems of optional keys. They do not stop the
	// agent: the agent writes them to its log at start.
	Warnings []string
}

// Offset is the time after each full minute at which the agent works. It
// spreads the reports of the fleet over the minute, and it does not change
// between restarts.
func (c Config) Offset() time.Duration {
	return time.Duration(c.ServerID%60) * time.Second
}

// ConfigFromEnv reads the configuration from the environment. The error names
// each key that is not set or not valid.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	var cfg Config
	var errs []error

	u, err := parseURL(getenv(EnvURL))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.URL = u

	cfg.Token = getenv(EnvToken)
	switch {
	case cfg.Token == "":
		errs = append(errs, fmt.Errorf("%s is not set", EnvToken))
	case strings.ContainsFunc(cfg.Token, func(r rune) bool { return r < '!' || r > '~' }):
		// The token is opaque, but it goes in a header: only printable ASCII
		// (the format is flyagt_ and base62) is safe there.
		errs = append(errs, fmt.Errorf("%s may contain only printable ASCII characters, without spaces", EnvToken))
	}

	if v := getenv(EnvServerID); v == "" {
		errs = append(errs, fmt.Errorf("%s is not set", EnvServerID))
	} else if id, err := strconv.ParseInt(v, 10, 64); err != nil || id < 0 {
		errs = append(errs, fmt.Errorf("%s must be an integer of 0 or more, not %q", EnvServerID, v))
	} else {
		cfg.ServerID = id
	}

	dir, err := stateDir(getenv(EnvStateDir))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.StateDir = dir

	// A typo in an optional key must not stop the metrics.
	on, ok := parseSwitch(getenv(EnvAutoUpdate))
	if !ok {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s=%q is not on or off; auto-update stays on", EnvAutoUpdate, getenv(EnvAutoUpdate)))
	}
	cfg.AutoUpdate = on

	return cfg, errors.Join(errs...)
}

// parseSwitch reads an optional on or off value. An empty value is on. ok is
// false for a value that is not known; that value is also on.
func parseSwitch(v string) (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "true", "1", "yes":
		return true, true
	case "off", "false", "0", "no":
		return false, true
	default:
		return true, false
	}
}

// parseURL accepts an https URL. It also accepts http for a loopback host,
// for tests and local development: plain http never leaves the machine. The
// URL is a base URL only: no user, password, query or fragment. An error
// never shows a password.
func parseURL(v string) (*url.URL, error) {
	if v == "" {
		return nil, fmt.Errorf("%s is not set", EnvURL)
	}

	u, err := url.Parse(strings.TrimRight(v, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%s is not a valid URL", EnvURL)
	}

	switch {
	case u.User != nil:
		return nil, fmt.Errorf("%s must not contain a user or a password: %s", EnvURL, u.Redacted())
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return nil, fmt.Errorf("%s must not contain a query or a fragment: %s", EnvURL, u.Redacted())
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return nil, fmt.Errorf("%s must be an https URL, not %s", EnvURL, u.Redacted())
	}

	return u, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// stateDir returns the first directory of STATE_DIRECTORY. systemd separates
// the directories with ":" when a unit has more than one.
func stateDir(v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("%s is not set (systemd sets it for StateDirectory=)", EnvStateDir)
	}

	dir, _, _ := strings.Cut(v, ":")
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", EnvStateDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %s", EnvStateDir, dir)
	}

	return dir, nil
}
