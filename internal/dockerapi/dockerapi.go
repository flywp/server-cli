// Package dockerapi reads the Docker Engine API over its unix socket, with the
// Go standard library only. It sends two requests, GET /version and
// GET /containers/json, and never another one: access to the socket is root
// access on the server (FlyWP monitoring agent contract v0.5.0).
package dockerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// DefaultTimeout limits each request (contract v0.5.0).
const DefaultTimeout = 5 * time.Second

// maxBody limits the size of an answer.
const maxBody = 16 << 20

// Client reads the Docker Engine API. Use New.
type Client struct {
	// Timeout limits each request. Tests make it shorter.
	Timeout time.Duration

	hc *http.Client
}

// New returns a client for the socket at path, for example
// /var/run/docker.sock.
func New(path string) *Client {
	var d net.Dialer
	return &Client{
		Timeout: DefaultTimeout,
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return d.DialContext(ctx, "unix", path)
				},
				MaxIdleConns: 1,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Container is a running container.
type Container struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
}

// Version returns the version of the Docker Engine, for example 29.7.1.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"Version"`
	}
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Containers returns the running containers.
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var cs []Container
	if err := c.get(ctx, "/containers/json", &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("docker %s: %w", path, err)
	}
	return nil
}
