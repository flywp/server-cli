package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/version"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL + "/base")
	if err != nil {
		t.Fatal(err)
	}

	return NewClient(Config{URL: u, Token: testToken}, srv.Client())
}

func TestClientSendsTheContractHeaders(t *testing.T) {
	var got *http.Request
	var body map[string]int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"report_interval": 5}`))
	})

	var reply struct {
		ReportInterval int `json:"report_interval"`
	}
	if err := c.do(context.Background(), http.MethodPost, "agent/v1/metrics", map[string]int{"n": 1}, &reply); err != nil {
		t.Fatal(err)
	}

	if got.URL.Path != "/base/agent/v1/metrics" {
		t.Errorf("path = %q, want the contract path under the base URL", got.URL.Path)
	}
	checks := map[string]string{
		"Authorization": "Bearer " + testToken,
		"User-Agent":    "fly/" + version.Version,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
	for header, want := range checks {
		if v := got.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}
	if body["n"] != 1 {
		t.Errorf("body = %v, want the JSON of the request", body)
	}
	if reply.ReportInterval != 5 {
		t.Errorf("reply = %+v, want the decoded JSON", reply)
	}
}

func TestClientWithoutBody(t *testing.T) {
	var got *http.Request
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{}`))
	})

	if err := c.do(context.Background(), http.MethodGet, "agent/v1/commands", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet || got.Header.Get("Content-Type") != "" {
		t.Errorf("request = %s with Content-Type %q, want GET without a body", got.Method, got.Header.Get("Content-Type"))
	}
}

func TestClientStatusError(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		retryAfter string
		want       time.Duration
	}{
		{"unauthorized", http.StatusUnauthorized, "", 0},
		{"throttled", http.StatusTooManyRequests, "30", 30 * time.Second},
		{"unavailable", http.StatusServiceUnavailable, "", 0},
		{"bad gateway", http.StatusBadGateway, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.code)
			})

			err := c.do(context.Background(), http.MethodPost, "agent/v1/events", map[string]int{}, nil)
			var statusErr *StatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf("do() error = %v, want a *StatusError", err)
			}
			if statusErr.StatusCode != tt.code || statusErr.RetryAfter != tt.want {
				t.Errorf("StatusError = %+v, want code %d and wait %v", statusErr, tt.code, tt.want)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                              0,
		"120":                           2 * time.Minute,
		"-5":                            0,
		"soon":                          0,
		"Tue, 22 Sep 2026 10:01:30 GMT": 90 * time.Second,
		"Tue, 22 Sep 2026 09:00:00 GMT": 0,
	}

	for v, want := range tests {
		if got := retryAfter(v, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", v, got, want)
		}
	}
}
