package escpos

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestExtractCUPSImage_OdooSOAPPreservesImageAndCut(t *testing.T) {
	raw := []byte{0xFF, 0xAA, 0x00, 0x55}
	doc := []byte(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>
	<epos-print xmlns="http://www.epson-pos.com/schemas/2011/03/epos-print">
	<image width="16" height="2" align="center">` + base64.StdEncoding.EncodeToString(raw) +
		`</image><cut type="feed" /></epos-print></s:Body></s:Envelope>`)
	got, err := ExtractCUPSImage(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Width != 16 || got.Height != 2 || !got.Cut || !bytes.Equal(got.Bitmap, raw) {
		t.Fatalf("incorrect extracted receipt: %+v", got)
	}
}

func TestExtractCUPSImage_LegacyForNonImageJobs(t *testing.T) {
	cases := []string{
		`<epos-print><pulse /></epos-print>`,
		`<epos-print><text>Hi</text></epos-print>`,
		`<epos-print><image width="8" height="1">AA==</image><feed line="1"/></epos-print>`,
		`<epos-print><image width="8" height="1">AA==</image><image width="8" height="1">AA==</image></epos-print>`,
		`<epos-print><image width="9" height="1">AAA=</image></epos-print>`,
	}
	for _, doc := range cases {
		got, err := ExtractCUPSImage([]byte(doc))
		if err != nil || got != nil {
			t.Fatalf("expected direct ESC/POS fallback for %q; got %+v err=%v", doc, got, err)
		}
	}
}

func TestExtractCUPSImage_BadImageFailsBeforeSubmission(t *testing.T) {
	for _, doc := range []string{
		`<epos-print><image width="8" height="2">AA==</image></epos-print>`,
		`<epos-print><image width="8" height="1">INVALID!</image></epos-print>`,
		`<epos-print><image width="32768" height="9000">AA==</image></epos-print>`,
		`<invalid></invalid>`,
	} {
		if _, err := ExtractCUPSImage([]byte(doc)); err == nil {
			t.Fatalf("expected image decoding error: %q", doc)
		}
	}
}

func TestExtractCUPSImage_NoCut(t *testing.T) {
	got, err := ExtractCUPSImage([]byte(`<epos-print><image width="8" height="1">AA==</image></epos-print>`))
	if err != nil || got == nil || got.Cut {
		t.Fatalf("expected valid no-cut image, got %+v err=%v", got, err)
	}
}
