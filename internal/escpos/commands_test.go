package escpos

import (
	"encoding/base64"
	"testing"

	"epos-proxy/internal/testutil"
)

func boolRef(v bool) *bool {
	return &v
}

func TestBuildText_Alignments(t *testing.T) {
	tests := []struct {
		name     string
		align    string
		expected []byte
	}{
		{
			name:     "align left",
			align:    "left",
			expected: []byte{ESC, 0x61, 0x00, 'H', 'i'},
		},
		{
			name:     "align center",
			align:    "center",
			expected: []byte{ESC, 0x61, 0x01, 'H', 'i'},
		},
		{
			name:     "align right",
			align:    "right",
			expected: []byte{ESC, 0x61, 0x02, 'H', 'i'},
		},
		{
			name:     "no align",
			align:    "",
			expected: []byte{'H', 'i'},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildText("Hi", TextAttrs{Align: tc.align})
			testutil.ExpectedBytesEqual(t, got, tc.expected)
		})
	}
}

func TestBuildText_CharSize(t *testing.T) {
	tests := []struct {
		name     string
		dw       *bool
		dh       *bool
		expected []byte
	}{
		{
			name:     "double width only",
			dw:       boolRef(true),
			dh:       nil,
			expected: []byte{GS, 0x21, 0x10, 'A'},
		},
		{
			name:     "double height only",
			dw:       nil,
			dh:       boolRef(true),
			expected: []byte{GS, 0x21, 0x01, 'A'},
		},
		{
			name:     "both double width and height",
			dw:       boolRef(true),
			dh:       boolRef(true),
			expected: []byte{GS, 0x21, 0x11, 'A'},
		},
		{
			name:     "both false",
			dw:       boolRef(false),
			dh:       boolRef(false),
			expected: []byte{'A'},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildText("A", TextAttrs{DoubleWidth: tc.dw, DoubleHeight: tc.dh})
			testutil.ExpectedBytesEqual(t, got, tc.expected)
		})
	}
}

func TestBuildText_Font(t *testing.T) {
	tests := []struct {
		name     string
		font     string
		expected []byte
	}{
		{
			name:     "font a",
			font:     "font_a",
			expected: []byte{ESC, 0x4D, 0x00, 'X'},
		},
		{
			name:     "font b",
			font:     "font_b",
			expected: []byte{ESC, 0x4D, 0x01, 'X'},
		},
		{
			name:     "font c",
			font:     "font_c",
			expected: []byte{ESC, 0x4D, 0x02, 'X'},
		},
		{
			name:     "unknown font",
			font:     "font_z",
			expected: []byte{'X'},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildText("X", TextAttrs{Font: tc.font})
			testutil.ExpectedBytesEqual(t, got, tc.expected)
		})
	}
}

func TestBuildText_EmAndUnderline(t *testing.T) {
	tests := []struct {
		name     string
		attrs    TextAttrs
		expected []byte
	}{
		{
			name:     "em on",
			attrs:    TextAttrs{Em: boolRef(true)},
			expected: []byte{ESC, 0x45, 0x01, 'T'},
		},
		{
			name:     "em off",
			attrs:    TextAttrs{Em: boolRef(false)},
			expected: []byte{ESC, 0x45, 0x00, 'T'},
		},
		{
			name:     "underline on",
			attrs:    TextAttrs{Underline: boolRef(true)},
			expected: []byte{ESC, 0x2D, 0x01, 'U'},
		},
		{
			name:     "underline off",
			attrs:    TextAttrs{Underline: boolRef(false)},
			expected: []byte{ESC, 0x2D, 0x00, 'U'},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text := "T"
			if tc.name[:3] == "und" {
				text = "U"
			}

			got := BuildText(text, tc.attrs)
			testutil.ExpectedBytesEqual(t, got, tc.expected)
		})
	}
}

func TestBuildText_Combined(t *testing.T) {
	attrs := TextAttrs{
		Align:        "center",
		DoubleWidth:  boolRef(true),
		DoubleHeight: boolRef(true),
		Font:         "font_b",
		Em:           boolRef(true),
		Underline:    boolRef(true),
	}

	got := BuildText("Hello", attrs)
	expected := []byte{
		ESC, 0x61, 0x01, // align center
		GS, 0x21, 0x11, // double width & height
		ESC, 0x4D, 0x01, // font b
		ESC, 0x45, 0x01, // em on
		ESC, 0x2D, 0x01, // ul on
		'H', 'e', 'l', 'l', 'o',
	}
	testutil.ExpectedBytesEqual(t, got, expected)
}

func TestBuildPulse_DefaultEPOSSemantics(t *testing.T) {
	got, err := BuildPulse(PulseAttrs{})
	testutil.ExpectedNoError(t, err)

	expected := []byte{
		ESC, 0x3D, 0x01,
		ESC, 0x70, 0x00, 0x32, 0x32,
	}
	testutil.ExpectedBytesEqual(t, got, expected)
	testutil.ExpectedBytesEqual(t, got, CmdPulse)
}

func TestBuildPulse_ExplicitDrawerAndTime(t *testing.T) {
	got, err := BuildPulse(PulseAttrs{
		Drawer: "drawer_2",
		Time:   "pulse_500",
	})
	testutil.ExpectedNoError(t, err)

	expected := []byte{
		ESC, 0x3D, 0x01,
		ESC, 0x70, 0x01, 0xFA, 0xFA,
	}
	testutil.ExpectedBytesEqual(t, got, expected)
}

func TestBuildPulse_RejectsUnsupportedValues(t *testing.T) {
	_, err := BuildPulse(PulseAttrs{Drawer: "drawer_3"})
	testutil.ExpectedError(t, err)

	_, err = BuildPulse(PulseAttrs{Time: "pulse_600"})
	testutil.ExpectedError(t, err)
}

func TestBuildImage_LeftAlignment(t *testing.T) {
	bitmap := []byte{0xFF}
	b64 := base64.StdEncoding.EncodeToString(bitmap)

	got, err := BuildImage(b64, ImageAttrs{
		Align:  "left",
		Width:  8,
		Height: 1,
	})
	testutil.ExpectedNoError(t, err)

	expected := []byte{
		GS, 0x76, 0x30, 0x00,
		0x01, 0x00,
		0x01, 0x00,
		0xFF,
	}

	testutil.ExpectedBytesEqual(t, got, expected)
}

func TestBuildImage_Valid(t *testing.T) {
	bitmap := []byte{0xFF, 0x00, 0xAA, 0x55}
	b64 := base64.StdEncoding.EncodeToString(bitmap)

	attrs := ImageAttrs{
		Align:  "center",
		Width:  16,
		Height: 2,
	}

	got, err := BuildImage(b64, attrs)
	testutil.ExpectedNoError(t, err)

	expectedPrefix := []byte{
		ESC, 0x61, 0x01, // center align
		GS, 0x76, 0x30, 0x00,
		0x02, 0x00, // xL=2, xH=0
		0x02, 0x00, // yL=2, yH=0
	}
	expected := append(expectedPrefix, bitmap...)
	testutil.ExpectedBytesEqual(t, got, expected)
}

func TestBuildImage_RawBase64(t *testing.T) {
	bitmap := []byte{0x12, 0x34}
	b64 := base64.RawStdEncoding.EncodeToString(bitmap)

	attrs := ImageAttrs{
		Align:  "right",
		Width:  8,
		Height: 2,
	}

	got, err := BuildImage(b64, attrs)
	testutil.ExpectedNoError(t, err)

	expectedPrefix := []byte{
		ESC, 0x61, 0x02, // right align
		GS, 0x76, 0x30, 0x00,
		0x01, 0x00, // xL=1, xH=0
		0x02, 0x00, // yL=2, yH=0
	}
	expected := append(expectedPrefix, bitmap...)
	testutil.ExpectedBytesEqual(t, got, expected)

	_, err = BuildImage("!!invalid-base64!!", ImageAttrs{Width: 8, Height: 1})
	testutil.ExpectedError(t, err)
}

func TestBuildImage_DataHandling(t *testing.T) {
	// Data too short.
	bitmap := []byte{0x01, 0x02}
	b64 := base64.StdEncoding.EncodeToString(bitmap)

	_, err := BuildImage(b64, ImageAttrs{Width: 16, Height: 2})
	testutil.ExpectedError(t, err)

	// Data longer than expected is truncated.
	bitmap = []byte{0x01, 0x02, 0x99, 0x99}
	b64 = base64.StdEncoding.EncodeToString(bitmap)

	got, err := BuildImage(b64, ImageAttrs{Width: 8, Height: 2})
	testutil.ExpectedNoError(t, err)

	expectedPrefix := []byte{
		GS, 0x76, 0x30, 0x00,
		0x01, 0x00, // xL=1, xH=0
		0x02, 0x00, // yL=2, yH=0
		0x01, 0x02, // truncated data
	}
	testutil.ExpectedBytesEqual(t, got, expectedPrefix)

	// A 300-row image now fits in one raster command.
	bitmap = make([]byte, 300)
	for i := range bitmap {
		bitmap[i] = byte(i % 256)
	}
	b64 = base64.StdEncoding.EncodeToString(bitmap)

	got, err = BuildImage(b64, ImageAttrs{Width: 8, Height: 300})
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedLen(t, got, 8+len(bitmap))
	testutil.ExpectedBytesEqual(t, got[:8], []byte{GS, 0x76, 0x30, 0x00, 0x01, 0x00, 0x2C, 0x01})
	testutil.ExpectedBytesEqual(t, got[8:], bitmap)
}

// Verify that larger images preserve all bytes across raster command boundaries.
func TestBuildImage_SplitsAtConfiguredHeight(t *testing.T) {
	const bytesPerRow = 2
	height := maxImgSliceHeight + 1
	bitmap := make([]byte, height*bytesPerRow)
	for i := range bitmap {
		bitmap[i] = byte(i % 251)
	}

	got, err := BuildImage(base64.StdEncoding.EncodeToString(bitmap), ImageAttrs{
		Width:  16,
		Height: height,
	})
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedLen(t, got, len(bitmap)+16)

	firstDataEnd := 8 + maxImgSliceHeight*bytesPerRow
	testutil.ExpectedBytesEqual(t, got[:8], []byte{
		GS, 0x76, 0x30, 0x00,
		bytesPerRow, 0x00,
		byte(maxImgSliceHeight & 0xFF), byte(maxImgSliceHeight >> 8),
	})
	testutil.ExpectedBytesEqual(t, got[8:firstDataEnd], bitmap[:maxImgSliceHeight*bytesPerRow])
	testutil.ExpectedBytesEqual(t, got[firstDataEnd:firstDataEnd+8], []byte{
		GS, 0x76, 0x30, 0x00,
		bytesPerRow, 0x00,
		0x01, 0x00,
	})
	testutil.ExpectedBytesEqual(t, got[firstDataEnd+8:], bitmap[maxImgSliceHeight*bytesPerRow:])
}

func TestBuildImage_RejectsUnsafeDimensions(t *testing.T) {
	tests := []ImageAttrs{
		{Width: -1, Height: 1},
		{Width: 1, Height: -1},
		{Width: 0, Height: 1},
		{Width: 1, Height: 0},
		{Width: maxImageBytes*8 + 1, Height: 1},
		{Width: 8, Height: maxImageBytes + 1},
	}

	for _, attrs := range tests {
		if _, err := BuildImage("AA==", attrs); err == nil {
			t.Fatalf("expected unsafe image dimensions to be rejected: %+v", attrs)
		}
	}
}

// Verify Epson's GS ( L function 112 store / function 50 print byte layout.
func TestBuildImage_EpsonGraphicsExactBytes(t *testing.T) {
	bitmap := []byte{0xFF, 0x00, 0xAA, 0x55}
	got, err := BuildImageWithMode(base64.StdEncoding.EncodeToString(bitmap), ImageAttrs{
		Align: "center", Width: 16, Height: 2,
	}, ImageModeEpsonGraphics)
	testutil.ExpectedNoError(t, err)

	expected := []byte{
		ESC, 0x61, 0x01,
		GS, 0x28, 0x4C, 0x0E, 0x00, // pL/pH = 10 + 4 image bytes
		0x30, 0x70, 0x30, 0x01, 0x01, 0x31, // fn=112, 1:1 monochrome, color 1
		0x10, 0x00, 0x02, 0x00, // 16 x 2 dots
		0xFF, 0x00, 0xAA, 0x55,
		GS, 0x28, 0x4C, 0x02, 0x00, 0x30, 0x32, // fn=50 print
	}
	testutil.ExpectedBytesEqual(t, got, expected)
}

// Chunks must print all original raster rows exactly once and in order.
func TestBuildImage_EpsonGraphicsAcrossSlices(t *testing.T) {
	const rows = epsonGraphicsSliceHeight + 1
	bitmap := make([]byte, rows)
	for i := range bitmap {
		bitmap[i] = byte(i % 251)
	}
	got, err := BuildImageWithMode(base64.StdEncoding.EncodeToString(bitmap),
		ImageAttrs{Width: 8, Height: rows}, ImageModeEpsonGraphics)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedLen(t, got, len(bitmap)+2*(15+8))

	expectedFirst := []byte{GS, 0x28, 0x4C, 0xCA, 0x00,
		0x30, 0x70, 0x30, 0x01, 0x01, 0x31, 0x08, 0x00, 0xC0, 0x00}
	testutil.ExpectedBytesEqual(t, got[:15], expectedFirst)
	firstEnd := 15 + epsonGraphicsSliceHeight
	testutil.ExpectedBytesEqual(t, got[15:firstEnd], bitmap[:epsonGraphicsSliceHeight])
	testutil.ExpectedBytesEqual(t, got[firstEnd:firstEnd+8],
		[]byte{GS, 0x28, 0x4C, 0x02, 0x00, 0x30, 0x32})
	second := firstEnd + 8
	testutil.ExpectedBytesEqual(t, got[second:second+15], []byte{
		GS, 0x28, 0x4C, 0x0B, 0x00, 0x30, 0x70, 0x30, 0x01,
		0x01, 0x31, 0x08, 0x00, 0x01, 0x00,
	})
	testutil.ExpectedBytesEqual(t, got[second+15:second+16], bitmap[epsonGraphicsSliceHeight:])
	testutil.ExpectedBytesEqual(t, got[second+16:],
		[]byte{GS, 0x28, 0x4C, 0x02, 0x00, 0x30, 0x32})
}

// Wide graphics must reduce slice height to fit the 16-bit GS ( L length.
func TestBuildImage_EpsonGraphicsPayloadSizeLimit(t *testing.T) {
	const width = 64000
	const height = 9
	raw := make([]byte, (width/8)*height)
	for i := range raw {
		raw[i] = byte(i % 251)
	}
	got, err := BuildImageWithMode(base64.StdEncoding.EncodeToString(raw),
		ImageAttrs{Width: width, Height: height}, ImageModeEpsonGraphics)
	testutil.ExpectedNoError(t, err)

	const firstRows = 8 // floor((65535 - 10) / (width / 8))
	const firstData = firstRows * (width / 8)
	second := 15 + firstData + 8
	testutil.ExpectedLen(t, got, len(raw)+2*(15+8))
	firstPayloadSize := int(got[3]) | int(got[4])<<8
	if firstPayloadSize != firstData+10 || firstPayloadSize > 65535 {
		t.Fatalf("invalid first GS ( L payload size %d", firstPayloadSize)
	}
	testutil.ExpectedBytesEqual(t, got[15:15+firstData], raw[:firstData])
	testutil.ExpectedBytesEqual(t, got[second+15:second+15+width/8], raw[firstData:])
}

func TestBuildImage_EpsonGraphicsRejectsUnsafeModesAndWidth(t *testing.T) {
	_, err := BuildImageWithMode("AA==", ImageAttrs{Width: 8, Height: 1}, ImageMode("unknown"))
	testutil.ExpectedError(t, err)
	_, err = BuildImageWithMode("AA==", ImageAttrs{Width: 65536, Height: 1}, ImageModeEpsonGraphics)
	testutil.ExpectedError(t, err)
}
