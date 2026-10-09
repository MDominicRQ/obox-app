package util

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MacAutostartEntryMatches verifies the LaunchAgent that go-autostart writes.
// The library's IsEnabled only checks file existence, so a stale path can
// remain "enabled" after moving or replacing the application bundle.
func MacAutostartEntryMatches(name string, command []string) bool {
	if name == "" || len(command) == 0 || !filepath.IsAbs(command[0]) {
		return false
	}
	path := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", name+".plist")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	var label string
	var registered []string
	var runAtLoad, foundLabel, foundArgs, foundRunAtLoad bool

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
		key, ok := token.(xml.StartElement)
		if !ok || key.Name.Local != "key" {
			continue
		}
		var keyName string
		if err := decoder.DecodeElement(&keyName, &key); err != nil {
			return false
		}
		value, err := macPlistValueStart(decoder)
		if err != nil {
			return false
		}

		switch keyName {
		case "Label":
			if value.Name.Local != "string" || decoder.DecodeElement(&label, &value) != nil {
				return false
			}
			foundLabel = true
		case "ProgramArguments":
			if value.Name.Local != "array" {
				return false
			}
			var args struct {
				Strings []string `xml:"string"`
			}
			if decoder.DecodeElement(&args, &value) != nil {
				return false
			}
			registered = args.Strings
			foundArgs = true
		case "RunAtLoad":
			runAtLoad = value.Name.Local == "true"
			foundRunAtLoad = true
			if err := decoder.Skip(); err != nil {
				return false
			}
		default:
			if err := decoder.Skip(); err != nil {
				return false
			}
		}
	}

	if !foundLabel || !foundArgs || !foundRunAtLoad || !runAtLoad ||
		label != name || len(registered) != len(command) {
		return false
	}
	for i, arg := range command {
		if registered[i] != arg {
			return false
		}
	}
	return true
}

// macPlistValueStart skips indentation between a plist key and its value.
func macPlistValueStart(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			return v, nil
		case xml.CharData:
			if strings.TrimSpace(string(v)) == "" {
				continue
			}
		}
		return xml.StartElement{}, io.ErrUnexpectedEOF
	}
}

// MacExecutableIsTranslocated detects temporary Gatekeeper paths which are
// unsuitable for a persistent login item.
func MacExecutableIsTranslocated(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/AppTranslocation/")
}
