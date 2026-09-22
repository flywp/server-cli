package docker

import (
	"testing"

	"github.com/flywp/server-cli/internal/testutil"
)

func TestDefaultService(t *testing.T) {
	tests := []struct {
		services []string
		want     string
		wantErr  bool
	}{
		{services: []string{"php", "nginx"}, want: "php"},
		{services: []string{"openlitespeed"}, want: "openlitespeed"},
		{services: []string{"php", "openlitespeed"}, want: "php"},
		{services: []string{"nginx"}, wantErr: true},
	}

	for _, tt := range tests {
		composePath := testutil.WriteSite(t, t.TempDir(), tt.services...)

		got, err := DefaultService(composePath)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("DefaultService(%q) = %q, %v, want %q (error: %v)", tt.services, got, err, tt.want, tt.wantErr)
		}
	}
}
