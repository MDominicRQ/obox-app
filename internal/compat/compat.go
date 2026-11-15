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
// that permission: Odoo's init_lna.js sets odoo.use_lna = false, so the scheme
// of the Odoo page wins. When that scheme does not match the listener the user
// copied, the request fails at the TLS handshake before the certificate is
// ever validated.
package compat

import "fmt"

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
)

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
