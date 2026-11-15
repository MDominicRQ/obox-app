package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMacOSGuideDocumentsFirefoxMatrix keeps the user-facing guide aligned
// with the code-level matrix. If a row, a UI label or a Firefox step
// disappears from docs/macos11-odoo-https.md, this test fails instead of
// shipping a guide that contradicts the integration or the app.
func TestMacOSGuideDocumentsFirefoxMatrix(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "macos11-odoo-https.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(data)

	required := []string{
		"## Firefox and Safari",
		"security.enterprise_roots.enabled",
		"Always Trust",
		"Trust this CA to identify websites",
		"SEC_ERROR_UNKNOWN_ISSUER",
		"SSL_ERROR_RX_RECORD_TOO_LONG",
		// Labels must match PrinterListItem.tsx exactly.
		"Local HTTP / LNA ON",
		"Local HTTPS / LNA OFF",
		"LAN HTTP / LNA ON",
		"LAN HTTPS / LNA OFF",
		// The reset in initLNA() happens after the printers are created.
		"Use Local Network Access",
		"mixed content",
		"https://127.0.0.1:4645/",
		odoo19Commit,
		"| F1 |",
		"| F2 |",
		"| F3 |",
		"| F4 |",
		"| F5 |",
		"| F6 |",
		"| F7 |",
		"| F8 |",
		"| F9 |",
	}

	for _, want := range required {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not mention %q", path, want)
		}
	}

	// The app never had Open HTTP / Open HTTPS buttons; the guide must not
	// promise them again.
	for _, forbidden := range []string{"**Open HTTP**", "**Open HTTPS**"} {
		if strings.Contains(doc, forbidden) {
			t.Errorf("%s references %q, which does not exist in the UI", path, forbidden)
		}
	}
}
