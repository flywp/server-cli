package cmd

import (
	"slices"
	"testing"
)

func TestSplitService(t *testing.T) {
	tests := []struct {
		args        []string
		wantService string
		wantCommand []string
	}{
		{args: []string{"php", "ls", "-la"}, wantService: "php", wantCommand: []string{"ls", "-la"}},
		{args: []string{"nginx", "nginx", "-t"}, wantService: "nginx", wantCommand: []string{"nginx", "-t"}},
		{args: []string{"openlitespeed", "ls"}, wantService: "openlitespeed", wantCommand: []string{"ls"}},
		{args: []string{"ls", "-la"}, wantService: "", wantCommand: []string{"ls", "-la"}},
		{args: []string{"php"}, wantService: "php", wantCommand: []string{}},
	}

	for _, tt := range tests {
		service, command := splitService(tt.args)
		if service != tt.wantService || !slices.Equal(command, tt.wantCommand) {
			t.Errorf("splitService(%q) = %q, %q, want %q, %q", tt.args, service, command, tt.wantService, tt.wantCommand)
		}
	}
}
