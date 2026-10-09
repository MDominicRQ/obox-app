package util

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

// MacAutostartEntryMatches verifies the LaunchAgent that go-autostart writes.
// The upstream IsEnabled method only checks file existence, so a stale path
// remains "enabled" after moving or replacing the application bundle.
func MacAutostartEntryMatches(name string, command []string) bool {
	if name == "" || len(command) == 0 || !filepath.IsAbs(command[0]) {
		return false
	}
	path := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", name+".plist")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var plist struct {
		XMLName xml.Name `xml:"plist"`
		Dict    struct {
			Keys    []string `xml:"key"`
			Strings []string `xml:"string"`
			Arrays  []struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
			True []struct{} `xml:"true"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return false
	}

	// The go-autostart template contains one top-level string (Label),
	// one array (ProgramArguments) and RunAtLoad=true.
	if plist.XMLName.Local != "plist" || len(plist.Dict.Strings) != 1 ||
		plist.Dict.Strings[0] != name || len(plist.Dict.Arrays) != 1 ||
		len(plist.Dict.True) == 0 ||
		!hasMacPlistKey(plist.Dict.Keys, "Label") ||
		!hasMacPlistKey(plist.Dict.Keys, "ProgramArguments") ||
		!hasMacPlistKey(plist.Dict.Keys, "RunAtLoad") {
		return false
	}

	registered := plist.Dict.Arrays[0].Strings
	if len(registered) != len(command) {
		return false
	}
	for i, arg := range command {
		if registered[i] != arg {
			return false
		}
	}
	return true
}

func hasMacPlistKey(keys []string, expected string) bool {
	for _, key := range keys {
		if key == expected {
			return true
		}
	}
	return false
}

// MacExecutableIsTranslocated detects a temporary Gatekeeper App Translocation
// path, which cannot be relied on for a future login.
func MacExecutableIsTranslocated(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/AppTranslocation/")
}
