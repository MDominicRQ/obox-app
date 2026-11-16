package trace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"epos-proxy/internal/escpos"
	"epos-proxy/internal/testutil"
)

// parseFields splits a trace line into key/value pairs. It understands the
// quoted error field so tests can assert whole values.
func parseFields(line string) map[string]string {
	fields := strings.Fields(line)
	out := make(map[string]string, len(fields))
	for i := 0; i < len(fields); i++ {
		if !strings.Contains(fields[i], "=") {
			continue
		}
		key, value, _ := strings.Cut(fields[i], "=")
		if strings.HasPrefix(value, "\"") && !strings.HasSuffix(value, "\"") {
			for i+1 < len(fields) {
				i++
				value += " " + fields[i]
				if strings.HasSuffix(fields[i], "\"") {
					break
				}
			}
		}
		out[key] = value
	}
	return out
}

func TestRequestIDKeepsUsableClientValue(t *testing.T) {
	testutil.ExpectedEqual(t, RequestID(" bench-01/A "), "bench-01_A")
}

func TestRequestIDGeneratesUniqueValues(t *testing.T) {
	first := RequestID("")
	second := RequestID("")

	testutil.ExpectedEqual(t, len(first), 2*requestIDEntropy)
	testutil.ExpectedNotEqual(t, first, second)
	testutil.ExpectedFalse(t, strings.ContainsAny(first, " <>\""))
}

func TestRequestIDTruncatesLongValues(t *testing.T) {
	testutil.ExpectedLen(t, []byte(RequestID(strings.Repeat("a", 100))), maxRequestIDLen)
}

func TestLineReportsEveryStage(t *testing.T) {
	tr := New("req-1", "LAN:127.0.0.1", TransportLAN, KindReceipt)
	tr.SetBody(3*time.Millisecond, 1024)
	tr.SetParse(7 * time.Millisecond)
	tr.SetImageMode(ImageModeLegacy)
	tr.SetRaster(576, 900, 1)
	tr.MarkEnqueue()
	tr.MarkJobStart()
	tr.MarkOpen(2*time.Millisecond, false)
	tr.AddWriteBlock(time.Millisecond)
	tr.AddWriteBlock(2 * time.Millisecond)
	tr.SetWrite(3*time.Millisecond, 4096, 4000)
	tr.MarkIdleClose(500 * time.Microsecond)
	tr.SetTotal(20 * time.Millisecond)

	got := parseFields(tr.Line())

	expected := map[string]string{
		"request_id":    "req-1",
		"kind":          KindReceipt,
		"printer_id":    "LAN:127.0.0.1",
		"transport":     TransportLAN,
		"image_mode":    ImageModeLegacy,
		"status":        statusOK,
		"code":          unknownField,
		"xml_bytes":     "1024",
		"escpos_bytes":  "4096",
		"device_bytes":  "4000",
		"write_blocks":  "2",
		"image_count":   "1",
		"raster_w":      "576",
		"raster_h":      "900",
		"body_ms":       "3.000",
		"parse_ms":      "7.000",
		"open_ms":       "2.000",
		"open_reused":   "false",
		"write_ms":      "3.000",
		"block_ms":      "1.000,2.000",
		"close_ms":      "0.500",
		"close_done":    "true",
		"cups_lp_ms":    unknownField,
		"cups_drain_ms": unknownField,
		"total_ms":      "20.000",
	}
	for key, want := range expected {
		if got[key] != want {
			t.Errorf("field %q = %q, want %q (line: %s)", key, got[key], want, tr.Line())
		}
	}
	if _, ok := got["error"]; ok {
		t.Errorf("error field must be absent on success, got %q", got["error"])
	}
	if _, ok := got["enqueue_ms"]; !ok {
		t.Error("enqueue_ms must always be present")
	}
	if strings.HasPrefix(got["recv_at"], "-") {
		t.Errorf("recv_at must carry a timestamp, got %q", got["recv_at"])
	}
}

func TestLineMarksUnknownStages(t *testing.T) {
	tr := New("req-2", "czpOT05fRVhJU1RFTlRfU0VSSUFMCg", TransportUSB, KindReceipt)

	got := parseFields(tr.Line())
	for _, key := range []string{
		"body_ms", "parse_ms", "enqueue_ms", "open_ms", "write_ms",
		"block_ms", "close_ms", "total_ms",
	} {
		testutil.ExpectedEqual(t, got[key], unknownField, key)
	}
	testutil.ExpectedEqual(t, got["status"], statusOK)
	testutil.ExpectedEqual(t, got["close_done"], "false")
	testutil.ExpectedEqual(t, got["write_blocks"], "0")
	testutil.ExpectedEqual(t, got["image_mode"], ImageModeNone)
	testutil.ExpectedEqual(t, got["xml_bytes"], "0")
}

func TestLineKeepsErrorOnOneLine(t *testing.T) {
	tr := New("req-3", "LAN:10.0.0.5", TransportLAN, KindLabel)
	tr.SetCode("EX_BADPORT")
	tr.SetError(errors.New("boom\nsecond\tline"))

	line := tr.Line()
	testutil.ExpectedEqual(t, strings.Count(line, "\n"), 0)

	got := parseFields(line)
	testutil.ExpectedEqual(t, got["status"], statusError)
	testutil.ExpectedEqual(t, got["code"], "EX_BADPORT")
	testutil.ExpectedEqual(t, got["error"], `"boom second\tline"`)
}

func TestLineSanitizesIdentifiers(t *testing.T) {
	tr := New("a b", "LAN: 10.0.0.5", TransportLAN, KindReceipt)

	got := parseFields(tr.Line())
	testutil.ExpectedEqual(t, got["request_id"], "a_b")
	testutil.ExpectedEqual(t, got["printer_id"], "LAN:_10.0.0.5")
}

func TestLineUsesRawImageModeForLabels(t *testing.T) {
	tr := New("req-4", "", TransportUSB, KindLabel)
	tr.SetImageMode(ImageModeRaw)

	got := parseFields(tr.Line())
	testutil.ExpectedEqual(t, got["image_mode"], ImageModeRaw)
	testutil.ExpectedEqual(t, got["kind"], KindLabel)
	testutil.ExpectedEqual(t, got["printer_id"], unknownField)
}

func TestDetectImageMode(t *testing.T) {
	legacyThenGSL := []byte{
		0x1D, 0x76, 0x30, 0x00, 0x03, 0x00, 0x01, 0x00, // GS v 0, 3 bytes/row, 1 row
		0x1D, 0x28, 0x4C, // bitmap payload that looks like a GS ( L signature
		0x1D, 0x28, 0x4C, 0x0A, 0x00, 0x30, 0x70, 0x30, 0x01, 0x01, 0x31, 0x18, 0x00, 0x01, 0x00, // GS ( L store
	}
	tests := []struct {
		name string
		job  []byte
		want string
	}{
		{name: "no image", job: []byte("plain text"), want: ImageModeNone},
		{name: "empty", job: nil, want: ImageModeNone},
		{name: "legacy GS v 0", job: []byte{0x1D, 0x76, 0x30, 0x00, 0x01, 0x00, 0x02, 0x00, 0xFF, 0xFF}, want: ImageModeLegacy},
		{name: "Epson GS ( L store", job: []byte{0x1D, 0x28, 0x4C, 0x0A, 0x00, 0x30, 0x70, 0x30, 0x01, 0x01, 0x31, 0x18, 0x00, 0x01, 0x00}, want: ImageModeGSL},
		{name: "Epson GS ( L print", job: []byte{0x1D, 0x28, 0x4C, 0x02, 0x00, 0x30, 0x32}, want: ImageModeGSL},
		{name: "legacy payload contains GS ( L bytes", job: []byte{0x1D, 0x76, 0x30, 0x00, 0x03, 0x00, 0x01, 0x00, 0x1D, 0x28, 0x4C}, want: ImageModeLegacy},
		{name: "both prefers GS ( L", job: legacyThenGSL, want: ImageModeGSL},
		{name: "truncated GS ( L is ignored", job: []byte{0x1D, 0x28, 0x4C, 0x1E, 0x30, 0x70}, want: ImageModeNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.ExpectedEqual(t, DetectImageMode(tc.job), tc.want)
		})
	}
}

// TestDetectImageModeParsedLegacyBitmap reproduces the Tech Lead regression
// case: the bitmap bytes of a legacy GS v 0 ticket contain 1D 28 4C, which must
// not be mistaken for an Epson GS ( L command.
func TestDetectImageModeParsedLegacyBitmap(t *testing.T) {
	jobData, err := escpos.ParseXML([]byte(`<epos-print><image width="24" height="1">HShM</image></epos-print>`))
	testutil.ExpectedNoError(t, err)

	testutil.ExpectedEqual(t, DetectImageMode(jobData), ImageModeLegacy)
}

// TestDetectImageModeEpsonGraphicsStream checks the opt-in GS ( L path added by
// PR #3 against the trace classifier: a real generated stream must report gs_l,
// not legacy.
func TestDetectImageModeEpsonGraphicsStream(t *testing.T) {
	jobData, err := escpos.ParseXMLWithMode(
		[]byte(`<epos-print><image width="24" height="2">/wAA/wAA</image></epos-print>`),
		escpos.ImageModeEpsonGraphics,
	)
	testutil.ExpectedNoError(t, err)

	testutil.ExpectedEqual(t, DetectImageMode(jobData), ImageModeGSL)
}

func TestRasterSize(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantW    int
		wantH    int
		wantImgs int
	}{
		{
			name:     "no images",
			body:     `<epos-print><text>hello</text><cut /></epos-print>`,
			wantW:    0,
			wantH:    0,
			wantImgs: 0,
		},
		{
			name:     "two images accumulate height and keep max width",
			body:     `<epos-print><text>hi</text><image width="576" height="300">AAAA</image><image width="384" height="120" align="center">BBBB</image><cut /></epos-print>`,
			wantW:    576,
			wantH:    420,
			wantImgs: 2,
		},
		{
			name:     "attributes are case insensitive and may use single quotes",
			body:     `<epos-print><IMAGE Width="8" HEIGHT='10'>AAAA</IMAGE></epos-print>`,
			wantW:    8,
			wantH:    10,
			wantImgs: 1,
		},
		{
			name:     "missing attributes count as zero",
			body:     `<epos-print><image>AAAA</image></epos-print>`,
			wantW:    0,
			wantH:    0,
			wantImgs: 1,
		},
		{
			name:     "truncated tag is ignored",
			body:     `<epos-print><image width="8"`,
			wantW:    0,
			wantH:    0,
			wantImgs: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			width, height, count := RasterSize([]byte(tc.body))
			testutil.ExpectedEqual(t, width, tc.wantW)
			testutil.ExpectedEqual(t, height, tc.wantH)
			testutil.ExpectedEqual(t, count, tc.wantImgs)
		})
	}
}

func TestSettleWaitsOnlyForQueuedJobs(t *testing.T) {
	tr := New("req-5", "LAN:127.0.0.1", TransportLAN, KindReceipt)

	started := time.Now()
	tr.Settle(200 * time.Millisecond)
	testutil.ExpectedTrue(t, time.Since(started) < 50*time.Millisecond, "un-queued traces must not wait")

	tr.MarkQueued()
	started = time.Now()
	tr.Settle(200 * time.Millisecond)
	testutil.ExpectedTrue(t, time.Since(started) >= 150*time.Millisecond, "queued traces must wait for the printer loop")

	tr.Finish()
	started = time.Now()
	tr.Settle(200 * time.Millisecond)
	testutil.ExpectedTrue(t, time.Since(started) < 50*time.Millisecond, "finished traces must not wait")
}

func TestLineIsStable(t *testing.T) {
	tr := New("req-6", "LAN:127.0.0.1", TransportLAN, KindReceipt)

	first := tr.Line()
	testutil.ExpectedEqual(t, tr.Line(), first)
}

func TestRequestIDReplacesPunctuationOnlyValues(t *testing.T) {
	id := RequestID("!!!")
	testutil.ExpectedNotEqual(t, id, "___")
	testutil.ExpectedLen(t, []byte(id), 2*requestIDEntropy)
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	first := Fingerprint([]byte("<epos-print><text>ORDER #42</text></epos-print>"))
	second := Fingerprint([]byte("<epos-print><text>ORDER #42</text></epos-print>"))
	other := Fingerprint([]byte("<epos-print><text>ORDER #43</text></epos-print>"))

	testutil.ExpectedEqual(t, first, second)
	testutil.ExpectedNotEqual(t, first, other)
	testutil.ExpectedEqual(t, len(first), 16)
}

func TestLineReportsClientCancellation(t *testing.T) {
	body := []byte("<epos-print><text>ORDER #42</text></epos-print>")
	tr := New("req-cancel", "LAN:127.0.0.1", TransportLAN, KindReceipt)
	tr.SetBody(time.Millisecond, len(body))
	tr.SetFingerprint(Fingerprint(body))
	tr.SetWrite(2*time.Millisecond, 128, 128)
	tr.MarkClientCanceled(CancelStageAfterWrite)

	got := parseFields(tr.Line())

	testutil.ExpectedEqual(t, got["client_canceled"], "true")
	testutil.ExpectedEqual(t, got["cancel_stage"], CancelStageAfterWrite)
	testutil.ExpectedEqual(t, got["body_sha256"], Fingerprint(body))
	testutil.ExpectedEqual(t, got["status"], statusOK)
	testutil.ExpectedEqual(t, got["device_bytes"], "128")
}

func TestLineReportsNoCancellationByDefault(t *testing.T) {
	tr := New("req-ok", "LAN:127.0.0.1", TransportLAN, KindReceipt)

	got := parseFields(tr.Line())

	testutil.ExpectedEqual(t, got["client_canceled"], "false")
	testutil.ExpectedEqual(t, got["cancel_stage"], unknownField)
	testutil.ExpectedEqual(t, got["body_sha256"], unknownField)
	testutil.ExpectedFalse(t, tr.ClientCanceled())
}

func TestClientCanceledKeepsFirstStage(t *testing.T) {
	tr := New("req-first-stage", "LAN:127.0.0.1", TransportLAN, KindReceipt)

	tr.MarkClientCanceled(CancelStageOpening)
	tr.MarkClientCanceled(CancelStageAfterWrite)

	got := parseFields(tr.Line())
	testutil.ExpectedEqual(t, got["client_canceled"], "true")
	testutil.ExpectedEqual(t, got["cancel_stage"], CancelStageOpening)
}

func TestDeviceBytesStartsAtZero(t *testing.T) {
	tr := New("req-bytes", "LAN:127.0.0.1", TransportLAN, KindReceipt)
	testutil.ExpectedEqual(t, tr.DeviceBytes(), 0)

	tr.SetWrite(time.Millisecond, 100, 40)
	testutil.ExpectedEqual(t, tr.DeviceBytes(), 40)
}
