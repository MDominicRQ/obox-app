package util

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMacAutostartEntryMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(dir, "epos-proxy.plist")
	executable := filepath.Join(home, "Applications", "ePOS Proxy.app", "Contents", "MacOS", "ePOS Proxy")
	command := []string{executable, "--background"}

	writePlist := func(label, exe, flag string, runAtLoad bool) {
		t.Helper()
		runAtLoadXML := "<true/>"
		if !runAtLoad {
			runAtLoadXML = "<false/>"
		}
		data := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>%s</string></array>
<key>RunAtLoad</key>%s
<key>AbandonProcessGroup</key><true/>
</dict></plist>`, label, exe, flag, runAtLoadXML)
		if err := os.WriteFile(plistPath, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("missing entry must not be treated as enabled")
	}
	writePlist("epos-proxy", executable, "--background", true)
	if !MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("matching entry should be enabled")
	}

	writePlist("epos-proxy", "/tmp/old/ePOS Proxy", "--background", true)
	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("stale executable path must not be treated as enabled")
	}
	writePlist("epos-proxy", executable, "--minimized", true)
	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("wrong argument must not be treated as enabled")
	}
	writePlist("other-app", executable, "--background", true)
	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("wrong label must not be treated as enabled")
	}
	writePlist("epos-proxy", executable, "--background", false)
	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("RunAtLoad=false must not be treated as enabled")
	}
	if err := os.WriteFile(plistPath, []byte("<plist>broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if MacAutostartEntryMatches("epos-proxy", command) {
		t.Fatal("malformed plist must not be treated as enabled")
	}
	if MacAutostartEntryMatches("epos-proxy", []string{"relative", "--background"}) {
		t.Fatal("relative executable must not be accepted")
	}
}

func TestMacExecutableIsTranslocated(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Applications/ePOS Proxy.app/Contents/MacOS/ePOS Proxy", false},
		{"/private/var/folders/xy/AppTranslocation/ABC/d/ePOS Proxy.app/Contents/MacOS/ePOS Proxy", true},
	}
	for _, tt := range tests {
		if got := MacExecutableIsTranslocated(tt.path); got != tt.want {
			t.Errorf("MacExecutableIsTranslocated(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
