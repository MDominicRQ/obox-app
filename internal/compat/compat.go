// Package compat is the executable specification of the Odoo 19 printer URL
// rules documented in docs/macos11-odoo-https.md.
//
// Odoo's point_of_sale module builds the printer URL in
// utils/printer/epson_printer.js:
//
//	protocol = odoo.use_lna ? "http:" : window.location.protocol
//	address  = protocol + "//" + ip + "/cgi-bin/epos/service.cgi?devid=local_printer"
//
// In Chromium the "local-network-access" permission exists, so
// point_of_sale.use_lna can stay enabled. Firefox and Safari do not implement
// that permission: once initLNA() has run, Odoo's init_lna.js sets
// odoo.use_lna = false, so the scheme of the Odoo page wins. When that scheme
// does not match the listener the user copied, the request fails at the TLS
// handshake before the certificate is ever validated.
//
// The snippets above are pinned from odoo/odoo at
// commit a157617c921669c5c036509b341be6a07ad1069d (branch 19.0) inside
// testdata/odoo19. TestOdoo19SourceContract checks that pinned snapshot, not a
// live upstream repository: it will not notice an Odoo change until someone
// re-fetches the excerpts and updates the commit. The procedure is documented
// in testdata/odoo19/README.md.
//
// The value injected into the page comes from the ir.config_parameter system
// parameter "point_of_sale.use_lna" (controllers/main.py in 19.0); Odoo 19.1+
// also exposes it as a "Use Local Network Access" option on the printer form.
//
// Classify models the steady state, after initLNA() has run. That is not the
// state Odoo is in when it builds the URL: PosStore.setup() awaits
// initServerData() — which creates every EpsonPrinter — before it calls
// initLNA(). ClassifyAtLoad models that construction-time state, where
// odoo.use_lna still holds the value injected from the system parameter.
package compat

import (
	"fmt"
	"net"
	"strings"
)

// Browser families relevant to the Odoo 19 integration.
type Browser string

const (
	Chromium Browser = "chromium"
	Firefox  Browser = "firefox"
	Safari   Browser = "safari"
)

// Schemes accepted by EffectiveURL and Classify.
const (
	HTTP  = "http"
	HTTPS = "https"
)

// Outcome of a page/address/browser combination.
type Outcome string

const (
	// OutcomeOK means the URL scheme and the listener scheme match and the
	// request reaches ePOS Proxy over plain HTTP.
	OutcomeOK Outcome = "ok"
	// OutcomeCARequired means the request reaches the HTTPS listener but the
	// browser only completes it when the local CA is trusted.
	OutcomeCARequired Outcome = "ca-required"
	// OutcomeTLSOnPlaintext means a TLS handshake was sent to the HTTP port.
	// Firefox reports SSL_ERROR_RX_RECORD_TOO_LONG.
	OutcomeTLSOnPlaintext Outcome = "tls-on-plaintext"
	// OutcomePlaintextOnTLS means a plaintext HTTP request was sent to the TLS
	// port; the proxy answers 400 and the browser reports a protocol error.
	OutcomePlaintextOnTLS Outcome = "plaintext-on-tls"
	// OutcomeMixedContent means Odoo built an http:// URL for a LAN host while
	// the page is https:// and the browser is not Chromium, so the request is
	// blocked as mixed content before any connection or certificate check
	// happens. Loopback targets are potentially trustworthy and are not
	// blocked, so they never produce this outcome.
	OutcomeMixedContent Outcome = "mixed-content"
)

// HostClass separates loopback targets from LAN targets, because browsers only
// treat loopback as a potentially trustworthy origin for mixed-content
// purposes.
type HostClass string

const (
	// Loopback covers 127.0.0.0/8, ::1 and localhost.
	Loopback HostClass = "loopback"
	// LAN covers every other host, including private LAN IPv4 addresses.
	LAN HostClass = "lan"
)

// Target is one protocol-free address shown by the app (for example
// "127.0.0.1:4545/p/printer-id" or "192.168.1.77:4645/p/printer-id") together
// with the listener scheme it belongs to.
type Target struct {
	Address string
	Scheme  string
}

// Host classifies the target host the way Firefox and WebKit do for mixed
// content: loopback is potentially trustworthy, everything else is not.
func (t Target) Host() HostClass {
	return ClassifyHost(t.Address)
}

// ClassifyHost returns Loopback for 127.0.0.0/8, ::1 and localhost, and LAN for
// every other host. The address is the protocol-free value shown by the app, so
// any "/p/<printer-id>" suffix and the port are ignored. Host names are not
// resolved: only "localhost" is recognised as loopback, which matches the
// addresses the app actually displays.
func ClassifyHost(address string) HostClass {
	host := address
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if strings.HasPrefix(host, "[") {
		if i := strings.IndexByte(host, ']'); i > 1 {
			host = host[1:i]
		}
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return Loopback
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return Loopback
	}
	return LAN
}

// EffectiveUseLNA mirrors Odoo's init_lna.js: the local-network-access
// permission only exists in Chromium, so Firefox and Safari always end up with
// use_lna=false even when the Odoo parameter is enabled.
func EffectiveUseLNA(browser Browser, configured bool) bool {
	if browser != Chromium {
		return false
	}
	return configured
}

// EffectiveURL returns the URL Odoo 19 calls for the given page scheme,
// effective use_lna flag and protocol-free address (for example
// "127.0.0.1:4545/p/printer-id"). pageScheme is HTTP or HTTPS.
func EffectiveURL(pageScheme string, useLNA bool, address string) string {
	scheme := pageScheme
	if useLNA {
		scheme = HTTP
	}
	return fmt.Sprintf("%s://%s/cgi-bin/epos/service.cgi?devid=local_printer", scheme, address)
}

// Classify reports what happens when Odoo calls a listener of addressScheme
// from a page served over pageScheme. useLNA is the effective value after
// EffectiveUseLNA. Unknown schemes return an error.
func Classify(pageScheme string, useLNA bool, addressScheme string) (Outcome, error) {
	if !validScheme(pageScheme) || !validScheme(addressScheme) {
		return "", fmt.Errorf("compat: scheme must be %q or %q", HTTP, HTTPS)
	}

	effective := pageScheme
	if useLNA {
		effective = HTTP
	}

	switch {
	case effective == addressScheme && effective == HTTPS:
		return OutcomeCARequired, nil
	case effective == addressScheme:
		return OutcomeOK, nil
	case effective == HTTPS:
		return OutcomeTLSOnPlaintext, nil
	default:
		return OutcomePlaintextOnTLS, nil
	}
}

func validScheme(scheme string) bool {
	return scheme == HTTP || scheme == HTTPS
}

// ClassifyAtLoad reports what Odoo actually calls at POS load time. In Odoo
// 19.0 PosStore.setup() creates every EpsonPrinter inside initServerData()
// before initLNA() runs, so configuredLNA — the value injected from the
// point_of_sale.use_lna system parameter — is still the value EpsonPrinter
// reads. The reset performed by initLNA() only affects printers created later;
// it never rebinds the URL of an existing printer, and a reload repeats the
// same order.
//
// Mixed content depends on the host, not only on the schemes. Firefox and
// WebKit treat loopback (127.0.0.0/8, ::1, localhost) as potentially
// trustworthy, so an http:// loopback URL keeps loading from an https:// page
// and falls through to Classify, where the listener scheme decides the error.
// A LAN http:// URL is blocked as mixed content and never reaches the proxy.
// Chromium with the local-network-access permission is allowed to make the
// request in both cases, so it always keeps the Classify outcome.
func ClassifyAtLoad(browser Browser, configuredLNA bool, pageScheme string, target Target) (Outcome, error) {
	if !validScheme(pageScheme) || !validScheme(target.Scheme) {
		return "", fmt.Errorf("compat: scheme must be %q or %q", HTTP, HTTPS)
	}

	effective := pageScheme
	if configuredLNA {
		effective = HTTP
	}

	if browser != Chromium && pageScheme == HTTPS && effective == HTTP && target.Host() == LAN {
		return OutcomeMixedContent, nil
	}

	return Classify(pageScheme, configuredLNA, target.Scheme)
}
