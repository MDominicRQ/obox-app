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
//
// The snippets above are pinned from odoo/odoo at
// commit a157617c921669c5c036509b341be6a07ad1069d (branch 19.0) inside
// testdata/odoo19; TestOdoo19SourceContract fails if the excerpts change
// without the model being revisited.
//
// Classify models the steady state, after initLNA() has run. That is not the
// state Odoo is in when it builds the URL: PosStore.setup() awaits
// initServerData() — which creates every EpsonPrinter — before it calls
// initLNA(). ClassifyAtLoad models that construction-time state, where
// odoo.use_lna still holds the value injected into the page from the POS
// configuration.
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
	// OutcomeMixedContent means Odoo built an http:// URL while the page is
	// https:// and the browser is not Chromium, so the request is blocked as
	// mixed content before any connection or certificate check happens.
	OutcomeMixedContent Outcome = "mixed-content"
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

// ClassifyAtLoad reports what Odoo actually calls at POS load time. In Odoo
// 19.0 PosStore.setup() creates every EpsonPrinter inside initServerData()
// before initLNA() runs, so configuredLNA — the value injected from the POS
// configuration — is still the value EpsonPrinter reads. The reset performed
// by initLNA() only affects printers created later; it never rebinds the URL
// of an existing printer, and a reload repeats the same order.
//
// Firefox and Safari block http:// subresources of an https:// page as mixed
// content, so an enabled LNA option turns every HTTPS POS into an unreachable
// printer there. Chromium with the local-network-access permission is allowed
// to make that request, so it keeps the Classify outcome.
func ClassifyAtLoad(browser Browser, configuredLNA bool, pageScheme, addressScheme string) (Outcome, error) {
	if !validScheme(pageScheme) || !validScheme(addressScheme) {
		return "", fmt.Errorf("compat: scheme must be %q or %q", HTTP, HTTPS)
	}

	if browser != Chromium && configuredLNA && pageScheme == HTTPS {
		return OutcomeMixedContent, nil
	}

	return Classify(pageScheme, configuredLNA, addressScheme)
}
