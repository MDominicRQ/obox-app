package escpos

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// CUPSImage holds an exact monochrome receipt bitmap from Odoo's <image>
// payload. ESC/POS convention: set bits are black. The extraction is deliberately
// restricted to one image and an optional <cut/>; all other jobs use the
// existing ESC/POS route so text, cash drawers and custom XML keep working.
type CUPSImage struct {
	Width  int
	Height int
	Bitmap []byte
	Cut    bool
}

// ExtractCUPSImage returns (nil, nil) for XML requests that are not the
// supported single-image receipt shape. Bad XML/image data is an error.
func ExtractCUPSImage(body []byte) (*CUPSImage, error) {
	s := string(body)
	start := strings.Index(s, "<epos-print")
	end := strings.LastIndex(s, "</epos-print>")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no <epos-print> element found in request body")
	}
	items, err := parseEPOSPrintFragment(s[start : end+len("</epos-print>")])
	if err != nil {
		return nil, err
	}
	var img *xmlRawItem
	cut := false
	for i := range items {
		switch strings.ToLower(items[i].XMLName.Local) {
		case "image":
			if img != nil {
				return nil, nil
			}
			img = &items[i]
		case "cut":
			cut = true // Reproduced by the Epson CUPS driver.
		default:
			return nil, nil
		}
	}
	if img == nil {
		return nil, nil
	}
	attrs := attrMap(img.Attrs)
	w := parseInt(attrs["width"], 0)
	h := parseInt(attrs["height"], 0)
	// A PNG 1-bit scanline can be created without bit shifting only when
	// the image has a byte-aligned width. Otherwise use the legacy route.
	if w <= 0 || w%8 != 0 || h <= 0 {
		return nil, nil
	}
	rowBytes := w / 8
	if rowBytes <= 0 || rowBytes > maxImageBytes || h > maxImageBytes/rowBytes {
		return nil, fmt.Errorf("CUPS image exceeds safety limits")
	}
	expected := rowBytes * h
	encoded := strings.TrimSpace(img.Content)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("CUPS image base64 decode: %w", err)
		}
	}
	if len(raw) < expected {
		return nil, fmt.Errorf("CUPS image too short: expected %d, got %d", expected, len(raw))
	}
	return &CUPSImage{Width: w, Height: h, Bitmap: raw[:expected], Cut: cut}, nil
}
