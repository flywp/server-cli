package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/flywp/server-cli/internal/version"
)

// requestTimeout limits each request to the control plane.
const requestTimeout = 20 * time.Second

// maxReplySize limits the reply body that the agent reads.
const maxReplySize = 1 << 20

// Client sends requests to the control plane.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient returns a client for the control plane of cfg. A nil hc uses a
// client with the default timeout.
func NewClient(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}

	return &Client{base: cfg.URL, token: cfg.Token, http: hc}
}

// StatusError is a reply from the control plane that is not 200 OK.
type StatusError struct {
	StatusCode int
	// RetryAfter is the wait that the Retry-After header asks for, or 0.
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("control plane replied %d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// do sends in as JSON (no body when in is nil) to path and decodes a 200 reply
// into out (no decoding when out is nil). Another status is a *StatusError.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "fly/"+version.Version)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReplySize))
		return &StatusError{StatusCode: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReplySize)).Decode(out); err != nil {
		return fmt.Errorf("reading the reply of %s: %w", path, err)
	}

	return nil
}

// retryAfter reads a Retry-After value: a number of seconds or an HTTP date.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(s)*time.Second, 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}

	return 0
}
