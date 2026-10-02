package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestStartupWindowOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantState  options.WindowStartState
		wantHidden bool
	}{
		{name: "normal", wantState: options.Normal},
		{name: "background", args: []string{"--background"}, wantState: options.Normal, wantHidden: true},
		{name: "legacy minimized", args: []string{"--minimized"}, wantState: options.Minimised},
		{name: "background wins visibility", args: []string{"--minimized", "--background"}, wantState: options.Minimised, wantHidden: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, hidden := startupWindowOptions(tc.args)
			if state != tc.wantState {
				t.Fatalf("state = %v, want %v", state, tc.wantState)
			}
			if hidden != tc.wantHidden {
				t.Fatalf("hidden = %v, want %v", hidden, tc.wantHidden)
			}
		})
	}
}
