package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestExitCode(t *testing.T) {
	childErr := exec.Command("sh", "-c", "exit 7").Run()

	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStderr string
	}{
		{name: "success", err: nil, wantCode: 0},
		{name: "child exit status passes through silently", err: childErr, wantCode: 7},
		{name: "wrapped child exit status", err: fmt.Errorf("starting site: %w", childErr), wantCode: 7},
		{name: "other error is printed", err: errors.New("boom"), wantCode: 1, wantStderr: "Error: boom\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := exitCode(tt.err, &stderr); got != tt.wantCode {
				t.Errorf("exitCode() = %d, want %d", got, tt.wantCode)
			}
			if got := stderr.String(); !strings.Contains(got, tt.wantStderr) || (tt.wantStderr == "" && got != "") {
				t.Errorf("stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}
