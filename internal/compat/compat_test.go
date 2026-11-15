package compat

import (
	"fmt"
	"testing"
)

func TestEffectiveUseLNA(t *testing.T) {
	cases := []struct {
		name       string
		browser    Browser
		configured bool
		want       bool
	}{
		{"chromium LNA enabled", Chromium, true, true},
		{"chromium LNA disabled", Chromium, false, false},
		{"firefox ignores LNA", Firefox, true, false},
		{"firefox without LNA", Firefox, false, false},
		{"safari ignores LNA", Safari, true, false},
		{"safari without LNA", Safari, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveUseLNA(tc.browser, tc.configured); got != tc.want {
				t.Fatalf("EffectiveUseLNA(%q, %v) = %v, want %v", tc.browser, tc.configured, got, tc.want)
			}
		})
	}
}

func TestEffectiveURL(t *testing.T) {
	const httpAddress = "127.0.0.1:4545/p/test-printer"
	const httpsAddress = "127.0.0.1:4645/p/test-printer"

	cases := []struct {
		name       string
		pageScheme string
		useLNA     bool
		address    string
		want       string
	}{
		{
			name:       "firefox or safari on an HTTP page",
			pageScheme: HTTP,
			useLNA:     false,
			address:    httpAddress,
			want:       "http://127.0.0.1:4545/p/test-printer/cgi-bin/epos/service.cgi?devid=local_printer",
		},
		{
			name:       "firefox or safari on an HTTPS page",
			pageScheme: HTTPS,
			useLNA:     false,
			address:    httpsAddress,
			want:       "https://127.0.0.1:4645/p/test-printer/cgi-bin/epos/service.cgi?devid=local_printer",
		},
		{
			name:       "chromium with LNA always calls the HTTP port",
			pageScheme: HTTPS,
			useLNA:     true,
			address:    httpAddress,
			want:       "http://127.0.0.1:4545/p/test-printer/cgi-bin/epos/service.cgi?devid=local_printer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveURL(tc.pageScheme, tc.useLNA, tc.address); got != tc.want {
				t.Fatalf("EffectiveURL(%q, %v, %q) = %q, want %q", tc.pageScheme, tc.useLNA, tc.address, got, tc.want)
			}
		})
	}
}

// TestFirefoxMatrix covers the supported rows F1-F8 of the compatibility
// matrix in docs/macos11-odoo-https.md, where "Use Local Network Access" is
// disabled in the Odoo POS configuration. Firefox has no LNA, so both
// configured values converge on the same outcome once initLNA() has run.
func TestFirefoxMatrix(t *testing.T) {
	cases := []struct {
		id            string
		pageScheme    string
		addressScheme string
		want          Outcome
	}{
		{"F1", HTTP, HTTP, OutcomeOK},
		{"F2", HTTP, HTTPS, OutcomePlaintextOnTLS},
		{"F3", HTTP, HTTP, OutcomeOK},
		{"F4", HTTP, HTTPS, OutcomePlaintextOnTLS},
		{"F5", HTTPS, HTTP, OutcomeTLSOnPlaintext},
		{"F6", HTTPS, HTTPS, OutcomeCARequired},
		{"F7", HTTPS, HTTP, OutcomeTLSOnPlaintext},
		{"F8", HTTPS, HTTPS, OutcomeCARequired},
	}

	for _, tc := range cases {
		// After initLNA() Firefox never has LNA, so both configured values
		// converge on the same outcome; the construction-time case where the
		// configured value still applies is covered by TestClassifyAtLoad.
		for _, configured := range []bool{true, false} {
			name := fmt.Sprintf("%s lna=%v", tc.id, configured)
			t.Run(name, func(t *testing.T) {
				useLNA := EffectiveUseLNA(Firefox, configured)
				if useLNA {
					t.Fatalf("Firefox must ignore the LNA parameter")
				}
				got, err := Classify(tc.pageScheme, useLNA, tc.addressScheme)
				if err != nil {
					t.Fatalf("Classify(%q, %v, %q): %v", tc.pageScheme, useLNA, tc.addressScheme, err)
				}
				if got != tc.want {
					t.Fatalf("Classify(%q, %v, %q) = %q, want %q", tc.pageScheme, useLNA, tc.addressScheme, got, tc.want)
				}
			})
		}
	}
}

// TestChromiumMatrix covers rows C1-C5 of the compatibility matrix, the
// Chromium control that explains why one address cannot serve every browser.
func TestChromiumMatrix(t *testing.T) {
	cases := []struct {
		id            string
		pageScheme    string
		configuredLNA bool
		addressScheme string
		want          Outcome
	}{
		{"C1", HTTP, true, HTTP, OutcomeOK},
		{"C2", HTTPS, true, HTTP, OutcomeOK},
		{"C3", HTTPS, true, HTTPS, OutcomePlaintextOnTLS},
		{"C4", HTTPS, false, HTTPS, OutcomeCARequired},
		{"C5", HTTPS, false, HTTP, OutcomeTLSOnPlaintext},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			useLNA := EffectiveUseLNA(Chromium, tc.configuredLNA)
			got, err := Classify(tc.pageScheme, useLNA, tc.addressScheme)
			if err != nil {
				t.Fatalf("Classify(%q, %v, %q): %v", tc.pageScheme, useLNA, tc.addressScheme, err)
			}
			if got != tc.want {
				t.Fatalf("Classify(%q, %v, %q) = %q, want %q", tc.pageScheme, useLNA, tc.addressScheme, got, tc.want)
			}
		})
	}
}

func TestClassifyRejectsUnknownSchemes(t *testing.T) {
	for _, tc := range []struct {
		page    string
		address string
	}{
		{"ftp", HTTP},
		{HTTP, "https:"},
		{"", HTTP},
	} {
		if _, err := Classify(tc.page, false, tc.address); err == nil {
			t.Fatalf("Classify(%q, false, %q) accepted an invalid scheme", tc.page, tc.address)
		}
		target := Target{Address: "127.0.0.1:4545/p/printer", Scheme: tc.address}
		if _, err := ClassifyAtLoad(Firefox, true, tc.page, target); err == nil {
			t.Fatalf("ClassifyAtLoad(Firefox, true, %q, %+v) accepted an invalid scheme", tc.page, target)
		}
	}
}

func TestClassifyHost(t *testing.T) {
	cases := []struct {
		address string
		want    HostClass
	}{
		{"127.0.0.1:4545/p/printer-id", Loopback},
		{"127.0.0.2:4545/p/printer-id", Loopback},
		{"127.1.2.3:4645/p/printer-id", Loopback},
		{"localhost:4545/p/printer-id", Loopback},
		{"LOCALHOST:4545", Loopback},
		{"[::1]:4545/p/printer-id", Loopback},
		{"::1", Loopback},
		{"127.0.0.1", Loopback},
		{"192.168.1.77:4545/p/printer-id", LAN},
		{"10.0.0.5:4645/p/printer-id", LAN},
		{"172.16.4.2:4545/p/printer-id", LAN},
		{"[fd00::1]:4545/p/printer-id", LAN},
		{"epos.local:4545/p/printer-id", LAN},
		{"", LAN},
	}

	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			if got := ClassifyHost(tc.address); got != tc.want {
				t.Fatalf("ClassifyHost(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}

// TestClassifyAtLoad covers the load-time state, where Odoo builds the printer
// URL in EpsonPrinter.setup() before initLNA() can clear odoo.use_lna. Mixed
// content is decided per host: browsers treat loopback as potentially
// trustworthy, so F9 must be split from the LAN case.
func TestClassifyAtLoad(t *testing.T) {
	const (
		localHTTP  = "127.0.0.1:4545/p/printer-id"
		localHTTPS = "127.0.0.1:4645/p/printer-id"
		lanHTTP    = "192.168.1.77:4545/p/printer-id"
		lanHTTPS   = "192.168.1.77:4645/p/printer-id"
	)

	cases := []struct {
		name          string
		browser       Browser
		configuredLNA bool
		pageScheme    string
		address       string
		addressScheme string
		want          Outcome
	}{
		// F9: LNA still enabled at construction time on an HTTPS page. Loopback
		// is potentially trustworthy, so the http:// request is not blocked and
		// the listener scheme decides the outcome.
		{"F9 firefox local HTTP stays allowed", Firefox, true, HTTPS, localHTTP, HTTP, OutcomeOK},
		{"F9 safari local HTTP stays allowed", Safari, true, HTTPS, localHTTP, HTTP, OutcomeOK},
		{"F9 firefox local HTTPS reaches plaintext check", Firefox, true, HTTPS, localHTTPS, HTTPS, OutcomePlaintextOnTLS},
		// F10: a LAN address is not potentially trustworthy, so the same http://
		// URL is blocked as mixed content before any connection.
		{"F10 firefox LAN HTTP is blocked", Firefox, true, HTTPS, lanHTTP, HTTP, OutcomeMixedContent},
		{"F10 firefox LAN HTTPS is blocked", Firefox, true, HTTPS, lanHTTPS, HTTPS, OutcomeMixedContent},
		{"F10 safari LAN HTTP is blocked", Safari, true, HTTPS, lanHTTP, HTTP, OutcomeMixedContent},
		// With LNA disabled, which is the documented setting for Firefox and
		// Safari, the page scheme wins again.
		{"F6 firefox LAN https with LNA disabled", Firefox, false, HTTPS, lanHTTPS, HTTPS, OutcomeCARequired},
		{"F5 firefox LAN http with LNA disabled", Firefox, false, HTTPS, lanHTTP, HTTP, OutcomeTLSOnPlaintext},
		{"F1 firefox http page with LNA enabled", Firefox, true, HTTP, lanHTTP, HTTP, OutcomeOK},
		// Chromium keeps LNA, so it is allowed to call the HTTP port from an
		// HTTPS page for both host classes; nothing is mixed content there.
		{"C2 chromium LAN http with LNA enabled", Chromium, true, HTTPS, lanHTTP, HTTP, OutcomeOK},
		{"C2 chromium local http with LNA enabled", Chromium, true, HTTPS, localHTTP, HTTP, OutcomeOK},
		{"C3 chromium LAN https with LNA enabled", Chromium, true, HTTPS, lanHTTPS, HTTPS, OutcomePlaintextOnTLS},
		{"C4 chromium LAN https with LNA disabled", Chromium, false, HTTPS, lanHTTPS, HTTPS, OutcomeCARequired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := Target{Address: tc.address, Scheme: tc.addressScheme}
			got, err := ClassifyAtLoad(tc.browser, tc.configuredLNA, tc.pageScheme, target)
			if err != nil {
				t.Fatalf("ClassifyAtLoad(%q, %v, %q, %+v): %v", tc.browser, tc.configuredLNA, tc.pageScheme, target, err)
			}
			if got != tc.want {
				t.Fatalf("ClassifyAtLoad(%q, %v, %q, %+v) = %q, want %q", tc.browser, tc.configuredLNA, tc.pageScheme, target, got, tc.want)
			}
		})
	}
}
