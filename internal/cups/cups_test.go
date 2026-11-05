package cups

import (
	"bytes"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"epos-proxy/internal/escpos"
)

func TestParseMatchingQueues_MustMatchExactSocketIP(t *testing.T) {
	text := `dispositivo para TMTIENDA: socket://192.168.12.33:9100
device for WRONG_IP: socket://192.168.12.34:9100
device for OTHER_PORT: socket://192.168.12.33:9101
device for USB: usb://Epson/TM-T88IV
device for SAFE: socket://192.168.12.33
device for INJECT;CMD: socket://192.168.12.33:9100`
	got := parseMatchingQueues(text, "192.168.12.33")
	expected := []string{"TMTIENDA", "SAFE"}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("incorrect queue selection: got %#v want %#v", got, expected)
	}
}

func TestEncode1BitPNG_PixelPerfect(t *testing.T) {
	img := &escpos.CUPSImage{Width: 16, Height: 3, Bitmap: []byte{
		0x00, 0xFF,
		0xAA, 0x55,
		0x80, 0x01,
	}}
	data, err := Encode1BitPNG(img, 180)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) ||
		!bytes.Contains(data, []byte("pHYs")) {
		t.Fatal("expected valid PNG signature and 180dpi metadata")
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decode generated 1-bit PNG: %v", err)
	}
	for row := 0; row < img.Height; row++ {
		for x := 0; x < img.Width; x++ {
			set := (img.Bitmap[row*(img.Width/8)+x/8] & (0x80 >> (x % 8))) != 0
			rr, gg, bb, _ := decoded.At(x, row).RGBA()
			expected := uint32(65535)
			if set {
				expected = 0
			}
			if rr != expected || gg != expected || bb != expected {
				t.Fatalf("pixel %d,%d changed; expected %d got %d,%d,%d", x, row, expected, rr, gg, bb)
			}
		}
	}
}

func TestEncode1BitPNG_RejectsMalformedInput(t *testing.T) {
	cases := []*escpos.CUPSImage{
		nil,
		{Width: 0, Height: 1, Bitmap: []byte{0}},
		{Width: 9, Height: 1, Bitmap: []byte{0}},
		{Width: 8, Height: 2, Bitmap: []byte{0}},
	}
	for _, image := range cases {
		_, err := Encode1BitPNG(image, 180)
		if err == nil {
			t.Fatalf("expected error for %+v", image)
		}
	}
	_, err := Encode1BitPNG(&escpos.CUPSImage{Width: 8, Height: 1, Bitmap: []byte{0}}, 0)
	if err == nil {
		t.Fatal("zero DPI must be rejected")
	}
}

func TestParseMatchingQueues_NoArbitraryLPCommands(t *testing.T) {
	if safeQueue.MatchString("foo;rm -rf /") || safeQueue.MatchString(strings.Repeat("x", 128)) {
		t.Fatal("queue names must not accept shell syntax or unbounded input")
	}
}
