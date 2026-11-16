package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// odoo19Commit is the odoo/odoo commit the excerpts in testdata/odoo19 were
// taken from. When it changes, re-fetch the three files, update the fixtures
// and re-check the matrix instead of only editing this constant.
const odoo19Commit = "a157617c921669c5c036509b341be6a07ad1069d"

// TestOdoo19SourceContract keeps the model anchored to a pinned snapshot of
// the real upstream code rather than to a free-standing specification. It does
// not query github.com: a change in Odoo is only detected after the fixtures
// are re-fetched following testdata/odoo19/README.md. The fixtures are verbatim
// excerpts of odoo/odoo (LGPL-3.0) pinned at odoo19Commit.
func TestOdoo19SourceContract(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		path := filepath.Join("testdata", "odoo19", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(data)
		if !strings.Contains(body, odoo19Commit) {
			t.Fatalf("%s is not pinned to odoo/odoo %s", path, odoo19Commit)
		}
		return body
	}

	epson := read("epson_printer.js")
	for _, want := range []string{
		`const protocol = odoo.use_lna ? "http:" : window.location.protocol;`,
		`this.url = protocol + "//" + ip;`,
		`this.address = this.url + "/cgi-bin/epos/service.cgi?devid=local_printer";`,
	} {
		if !strings.Contains(epson, want) {
			t.Errorf("epson_printer.js no longer contains %q", want)
		}
	}

	initLNA := read("init_lna.js")
	for _, want := range []string{
		`navigator.permissions.query({ name: "local-network-access" })`,
		`odoo.use_lna = false;`,
	} {
		if !strings.Contains(initLNA, want) {
			t.Errorf("init_lna.js no longer contains %q", want)
		}
	}

	posStore := read("pos_store.js")
	if !strings.Contains(posStore, `await this.processServerData();`) {
		t.Errorf("pos_store.js no longer shows initServerData() awaiting processServerData()")
	}
	if !strings.Contains(posStore, `const HWPrinter = this.createPrinter(printer);`) {
		t.Errorf("pos_store.js no longer creates printers inside processServerData()")
	}
	if !strings.Contains(posStore, `new EpsonPrinter({ ip: config.epson_printer_ip })`) {
		t.Errorf("pos_store.js no longer constructs EpsonPrinter from the POS configuration")
	}

	// The whole point of ClassifyAtLoad: printers exist before initLNA() runs.
	initServer := strings.Index(posStore, `await this.initServerData();`)
	initLNAAt := strings.Index(posStore, `initLNA(this.notification`)
	if initServer < 0 || initLNAAt < 0 {
		t.Fatalf("pos_store.js excerpt no longer shows setup() calling initServerData() and initLNA()")
	}
	if initServer > initLNAAt {
		t.Fatalf("pos_store.js now calls initLNA() before initServerData(); ClassifyAtLoad must be revisited")
	}
}
