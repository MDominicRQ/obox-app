package cups

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

const verifiedQueueOptions = `PageSize/Media Size: 72x200mm 80x297mm *RP80x297 Custom.WIDTHxLENGTH
Resolution/Output Resolution: 180x180dpi *360x360dpi
TmtSpeed/Print Speed: *Auto 1 2 3 4 5
TmtPaperReduction/Paper Reduction: *None Both Text
TmtPaperSource/Paper Source: *DocFeedNoCut DocFeedCut DocFeedCutDrawer
TmtDrawer1/Drawer 1: *Off On 100ms
TmtDrawer2/Drawer 2: *Off On 100ms
`

func stubDarwin(t *testing.T) {
	t.Helper()
	previous := requireDarwin
	requireDarwin = func() error { return nil }
	ForgetQueue("")
	t.Cleanup(func() {
		requireDarwin = previous
		ForgetQueue("")
	})
}

func TestInspectQueue_ParsesLocalizedLpoptionsOutput(t *testing.T) {
	stubDarwin(t)
	queueOptions = func(context.Context, string) ([]byte, error) {
		return []byte(verifiedQueueOptions), nil
	}
	profile, err := InspectQueue(context.Background(), "TMTIENDA")
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Has("Resolution", "180x180dpi") || !profile.Has("Resolution", "360x360dpi") {
		t.Fatalf("resolution choices not parsed: %#v", profile.Choices["Resolution"])
	}
	if !profile.Has("TmtPaperSource", "DocFeedCut") || !profile.Has(mediaOption, "RP80x297") {
		t.Fatalf("driver options not parsed: %#v", profile.Choices)
	}
	if profile.Has("media", "RP80x327") {
		t.Fatal("unknown media choice must not be accepted")
	}
}

func TestQueueProfile_RefusesQueueWithoutVerifiedResolution(t *testing.T) {
	profile := parseQueueProfile("OTHER", "Resolution/Output Resolution: *360x360dpi\n"+
		"TmtPaperSource/Paper Source: *DocFeedNoCut DocFeedCut\n"+
		"PageSize/Media Size: *RP80x297\n")
	err := profile.SupportsReceiptPrinting()
	if err == nil || !strings.Contains(err.Error(), "180x180dpi") {
		t.Fatalf("expected explicit rejection of a 360 dpi-only queue, got %v", err)
	}
	if _, err := profile.PrintOptions(100, true); err == nil {
		t.Fatal("print options must not be planned for a refused queue")
	}
}

func TestQueueProfile_RefusesQueueWithoutCutControl(t *testing.T) {
	profile := parseQueueProfile("NOCUT", "Resolution/Output Resolution: *180x180dpi\n"+
		"PageSize/Media Size: *RP80x297\n")
	if err := profile.SupportsReceiptPrinting(); err == nil || !strings.Contains(err.Error(), "cut") {
		t.Fatalf("expected missing cut control to be refused, got %v", err)
	}
}

func TestQueueProfile_RefusesUnknownProfileWithoutDriverOptions(t *testing.T) {
	profile := parseQueueProfile("RAW", "")
	if err := profile.SupportsReceiptPrinting(); err == nil {
		t.Fatal("a queue without PPD options must be refused")
	}
}

func TestQueueProfile_PrintOptionsFollowRealQueue(t *testing.T) {
	profile := parseQueueProfile("TMTIENDA", verifiedQueueOptions)
	args, err := profile.PrintOptions(120, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"Resolution=180x180dpi", "TmtPaperSource=DocFeedCut", "media=RP80x297", "TmtDrawer1=Off"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "=raw") || strings.Contains(joined, " raw ") {
		t.Fatalf("raw mode must never be used: %q", joined)
	}

	noCut, err := profile.PrintOptions(120, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(noCut, " "), "TmtPaperSource=DocFeedNoCut") {
		t.Fatalf("missing no-cut source: %v", noCut)
	}
}

func TestQueueProfile_LongReceiptUsesCustomMedia(t *testing.T) {
	profile := parseQueueProfile("TMTIENDA", verifiedQueueOptions)
	args, err := profile.PrintOptions(420, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "media=Custom.80x430mm") {
		t.Fatalf("long receipt must request custom media, got %v", args)
	}

	noCustom := parseQueueProfile("FIXED", "Resolution/Output Resolution: *180x180dpi\n"+
		"TmtPaperSource/Paper Source: *DocFeedNoCut DocFeedCut\n"+
		"PageSize/Media Size: *RP80x297\n")
	if _, err := noCustom.PrintOptions(420, true); err == nil {
		t.Fatal("a fixed-media queue must refuse a >297 mm receipt instead of cropping it")
	}
}

func TestQueueProfile_OmitsOptionsMissingFromPPD(t *testing.T) {
	profile := parseQueueProfile("MINIMAL", "Resolution/Output Resolution: *180x180dpi\n"+
		"TmtPaperSource/Paper Source: *DocFeedNoCut DocFeedCut\n"+
		"PageSize/Media Size: *RP80x297\n")
	args, err := profile.PrintOptions(100, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, unwanted := range []string{"TmtSpeed", "TmtPaperReduction", "TmtDrawer1", "TmtDrawer2", "ppi="} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("option %q is not exposed by the queue but was forced: %q", unwanted, joined)
		}
	}
}

func TestSubmit_RefusesForeignQueueAndNeverResends(t *testing.T) {
	stubDarwin(t)
	listQueues = func(context.Context) ([]byte, error) {
		return []byte("device for OTHER: socket://10.0.0.9:9100\n"), nil
	}
	submitted := false
	submitLP = func(context.Context, []string) ([]byte, error) {
		submitted = true
		return nil, nil
	}
	img := &escpos.CUPSImage{Width: 8, Height: 1, Bitmap: []byte{0x00}}
	if _, err := Submit(context.Background(), "192.168.12.33", "TMTIENDA", img); err == nil {
		t.Fatal("a queue pointing to another IP must be refused")
	}
	if submitted {
		t.Fatal("no lp job may be submitted for a foreign queue")
	}
}

func TestSubmit_UsesVerifiedProfileAndCustomMedia(t *testing.T) {
	stubDarwin(t)
	listQueues = func(context.Context) ([]byte, error) {
		return []byte("device for TMTIENDA: socket://192.168.12.33:9100\n"), nil
	}
	queueOptions = func(context.Context, string) ([]byte, error) {
		return []byte(verifiedQueueOptions), nil
	}
	var got []string
	submitLP = func(_ context.Context, args []string) ([]byte, error) {
		got = append([]string(nil), args...)
		return nil, nil
	}
	img := &escpos.CUPSImage{Width: 8, Height: 2500, Bitmap: make([]byte, 2500), Cut: true}
	if _, err := Submit(context.Background(), "192.168.12.33", "TMTIENDA", img); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-d TMTIENDA") || !strings.Contains(joined, "TmtPaperSource=DocFeedCut") {
		t.Fatalf("unexpected lp arguments: %v", got)
	}
	if !strings.Contains(joined, "media=Custom.80x") {
		t.Fatalf("expected custom media for a long ticket: %v", got)
	}
	if strings.Contains(joined, "-o raw") || strings.Contains(joined, " raw ") {
		t.Fatalf("raw mode must never be used: %v", got)
	}
}

func TestSubmit_SubmissionSurvivesClientCancellation(t *testing.T) {
	stubDarwin(t)
	listQueues = func(context.Context) ([]byte, error) {
		return []byte("device for TMTIENDA: socket://192.168.12.33:9100\n"), nil
	}
	queueOptions = func(context.Context, string) ([]byte, error) {
		return []byte(verifiedQueueOptions), nil
	}
	submitLP = func(ctx context.Context, args []string) ([]byte, error) {
		if ctx.Err() != nil {
			t.Fatalf("lp was started with an already canceled context")
		}
		return nil, nil
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	img := &escpos.CUPSImage{Width: 8, Height: 1, Bitmap: []byte{0x00}}
	if _, err := Submit(canceled, "192.168.12.33", "TMTIENDA", img); err != nil {
		t.Fatalf("a canceled client must not abort an already-authorized submission: %v", err)
	}
}

// Labelled simulated CUPS rejection: the job is reported to Odoo and never
// resubmitted, because a rejected or ambiguous job could still be queued.
func TestSubmit_RejectionIsReportedWithoutResend(t *testing.T) {
	stubDarwin(t)
	listQueues = func(context.Context) ([]byte, error) {
		return []byte("device for TMTIENDA: socket://192.168.12.33:9100\n"), nil
	}
	queueOptions = func(context.Context, string) ([]byte, error) {
		return []byte(verifiedQueueOptions), nil
	}
	calls := 0
	submitLP = func(context.Context, []string) ([]byte, error) {
		calls++
		return []byte("lp: The printer or class does not exist."), fmt.Errorf("exit status 1")
	}
	img := &escpos.CUPSImage{Width: 8, Height: 1, Bitmap: []byte{0x00}}
	_, err := Submit(context.Background(), "192.168.12.33", "TMTIENDA", img)
	if err == nil || !strings.Contains(err.Error(), "rejected job") {
		t.Fatalf("rejection must be surfaced to the caller, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("a rejected job must not be resent, got %d submissions", calls)
	}
}

// Labelled simulated CUPS timeout: an ambiguous acceptance is never retried.
func TestSubmit_TimeoutIsReportedWithoutResend(t *testing.T) {
	stubDarwin(t)
	listQueues = func(context.Context) ([]byte, error) {
		return []byte("device for TMTIENDA: socket://192.168.12.33:9100\n"), nil
	}
	queueOptions = func(context.Context, string) ([]byte, error) {
		return []byte(verifiedQueueOptions), nil
	}
	calls := 0
	submitLP = func(context.Context, []string) ([]byte, error) {
		calls++
		return nil, context.DeadlineExceeded
	}
	img := &escpos.CUPSImage{Width: 8, Height: 1, Bitmap: []byte{0x00}}
	_, err := Submit(context.Background(), "192.168.12.33", "TMTIENDA", img)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout must be surfaced to the caller, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("an ambiguous timeout must not trigger a resend, got %d submissions", calls)
	}
}
