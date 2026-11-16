// Package cups submits Epson receipts through a locally installed CUPS
// queue. It does not use raw mode: the Epson printer driver must process the
// 1-bit PNG, just as it does when printing through the macOS print dialog.
//
// The raster profile (180 dpi, 80 mm roll paper, driver cut control) was
// verified against one Epson TM-T88IV queue. Every other queue is inspected
// before use and rejected with an explicit error when it does not expose the
// same options, instead of silently applying that profile.
package cups

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"epos-proxy/internal/escpos"
)

const (
	// Keep the whole CUPS path well below Odoo's 15 s AbortSignal timeout so a
	// slow CUPS never turns into a client abort followed by a manual retry.
	cupsTimeout      = 10 * time.Second
	lpstatTimeout    = 3 * time.Second
	lpoptionsTimeout = 3 * time.Second
	defaultDPI       = 180
	maxPrintedHeight = 4 * 1024 * 1024
	// Validation results are reused briefly: a repointed queue is detected
	// within one minute, while normal tickets pay no extra process spawns.
	validationTTL = 60 * time.Second

	verifiedResolution = "180x180dpi"
	standardMedia      = "RP80x297"
	mediaOption        = "PageSize"
	paperSourceOption  = "TmtPaperSource"
)

var safeQueue = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,127}$`)

// Command hooks make the macOS-only commands testable on any platform.
var (
	listQueues   = func(ctx context.Context) ([]byte, error) { return run(ctx, "lpstat", "-v") }
	queueOptions = func(ctx context.Context, queue string) ([]byte, error) {
		return run(ctx, "lpoptions", "-p", queue, "-l")
	}
	submitLP = func(ctx context.Context, args []string) ([]byte, error) {
		return run(ctx, "lp", args...)
	}
)

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

var requireDarwin = func() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("local Epson CUPS mode is only enabled on macOS")
	}
	return nil
}

// MatchingQueues returns installed local CUPS queues using socket://IP:9100.
// It never silently chooses a printer with an unrelated device address.
func MatchingQueues(ctx context.Context, ip string) ([]string, error) {
	if err := requireDarwin(); err != nil {
		return nil, err
	}
	if queues, ok := cached(cachedQueueList, ip); ok {
		return queues, nil
	}
	c, cancel := context.WithTimeout(ctx, lpstatTimeout)
	defer cancel()
	out, err := listQueues(c)
	if err != nil {
		return nil, fmt.Errorf("cannot list CUPS queues: %w", err)
	}
	queues := parseMatchingQueues(string(out), ip)
	storeCached(cachedQueueList, ip, queues)
	return queues, nil
}

func parseMatchingQueues(lpstatOutput, ip string) []string {
	queues := make([]string, 0)
	for _, line := range strings.Split(lpstatOutput, "\n") {
		head, rawURL, found := strings.Cut(strings.TrimSpace(line), ": socket://")
		if !found {
			continue
		}
		fields := strings.Fields(head)
		if len(fields) == 0 {
			continue
		}
		queue := fields[len(fields)-1]
		if !safeQueue.MatchString(queue) {
			continue
		}
		address := "socket://" + strings.TrimSpace(rawURL)
		device, err := url.Parse(address)
		if err != nil || !strings.EqualFold(device.Scheme, "socket") {
			continue
		}
		port := device.Port()
		if device.Hostname() != ip || (port != "" && port != "9100") {
			continue
		}
		queues = append(queues, queue)
	}
	return queues
}

// QueueProfile is the set of driver options an installed CUPS queue exposes,
// as reported by `lpoptions -p <queue> -l`.
type QueueProfile struct {
	Queue   string
	Choices map[string]map[string]bool
}

// InspectQueue reads the real PPD options of a queue. A queue without Epson
// driver options (for example a raw queue) cannot be used and is rejected.
func InspectQueue(ctx context.Context, queue string) (*QueueProfile, error) {
	if err := requireDarwin(); err != nil {
		return nil, err
	}
	if !safeQueue.MatchString(queue) {
		return nil, fmt.Errorf("invalid CUPS printer queue name")
	}
	if profile, ok := cached(cachedProfiles, queue); ok {
		return profile, nil
	}
	c, cancel := context.WithTimeout(ctx, lpoptionsTimeout)
	defer cancel()
	out, err := queueOptions(c, queue)
	if err != nil {
		return nil, fmt.Errorf("cannot read CUPS queue %q options: %w", queue, err)
	}
	profile := parseQueueProfile(queue, string(out))
	if len(profile.Choices) == 0 {
		return nil, fmt.Errorf("CUPS queue %q exposes no driver options; install or verify the Epson driver and PPD", queue)
	}
	storeCached(cachedProfiles, queue, profile)
	return profile, nil
}

func parseQueueProfile(queue, output string) *QueueProfile {
	profile := &QueueProfile{Queue: queue, Choices: make(map[string]map[string]bool)}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "Option: ")
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if slash := strings.Index(name, "/"); slash >= 0 {
			name = name[:slash]
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		choices := make(map[string]bool)
		for _, field := range strings.Fields(rest) {
			choices[strings.TrimPrefix(field, "*")] = true
		}
		if len(choices) > 0 {
			profile.Choices[name] = choices
		}
	}
	return profile
}

// Has reports whether the queue exposes a choice for an option. An option that
// is not present at all returns false; callers decide which options are
// mandatory.
func (p *QueueProfile) Has(option, choice string) bool {
	if p == nil {
		return false
	}
	choices, ok := p.Choices[option]
	return ok && choices[choice]
}

func (p *QueueProfile) hasOption(option string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Choices[option]
	return ok
}

// SupportsReceiptPrinting verifies, before enabling the mode, that the queue
// can reproduce this 80 mm profile: 180 dpi, driver-controlled cut and some
// usable roll-paper media. A queue that only supports other values is refused
// rather than being printed with the TM-T88IV options.
func (p *QueueProfile) SupportsReceiptPrinting() error {
	if p == nil {
		return fmt.Errorf("CUPS queue profile is unknown")
	}
	if !p.hasOption("Resolution") {
		return fmt.Errorf("CUPS queue %q does not expose a Resolution option; the 180 dpi receipt profile is not verified for it", p.Queue)
	}
	if !p.Has("Resolution", verifiedResolution) {
		return fmt.Errorf("CUPS queue %q does not support %s (options: %s); keep the direct ESC/POS mode",
			p.Queue, verifiedResolution, p.choiceList("Resolution"))
	}
	if !p.hasOption(paperSourceOption) {
		return fmt.Errorf("CUPS queue %q does not expose %s driver cut control; CUPS mode would not guarantee a correct cut", p.Queue, paperSourceOption)
	}
	if !p.Has(paperSourceOption, "DocFeedCut") || !p.Has(paperSourceOption, "DocFeedNoCut") {
		return fmt.Errorf("CUPS queue %q does not expose DocFeedCut/DocFeedNoCut; CUPS mode would not guarantee a correct cut", p.Queue)
	}
	if !p.hasMedia(standardMedia) && !p.customMedia() {
		return fmt.Errorf("CUPS queue %q exposes neither %s nor a custom 80 mm media (options: %s)", p.Queue, standardMedia, p.choiceList(mediaOption))
	}
	return nil
}

func (p *QueueProfile) mediaChoices() map[string]bool {
	if p == nil {
		return nil
	}
	choices := make(map[string]bool, len(p.Choices[mediaOption])+len(p.Choices["media"]))
	for name, set := range p.Choices {
		if name == mediaOption || name == "media" {
			for choice := range set {
				choices[choice] = true
			}
		}
	}
	return choices
}

func (p *QueueProfile) hasMedia(choice string) bool {
	return p.mediaChoices()[choice]
}

func (p *QueueProfile) customMedia() bool {
	for choice := range p.mediaChoices() {
		// Only an 80 mm custom width, or the PPD's generic
		// Custom.WIDTHxLENGTH placeholder, justifies building Custom.80x<len>mm.
		if strings.HasPrefix(choice, "Custom.80x") || strings.HasPrefix(choice, "Custom.WIDTHx") {
			return true
		}
	}
	return false
}

func (p *QueueProfile) choiceList(option string) string {
	choices, ok := p.Choices[option]
	if !ok {
		return "none"
	}
	out := make([]string, 0, len(choices))
	for choice := range choices {
		out = append(out, choice)
	}
	return strings.Join(out, " ")
}

// PrintOptions returns the `lp -o` arguments for this ticket. Options absent
// from the queue's PPD are omitted instead of being forced, and a receipt that
// cannot be represented is an error.
func (p *QueueProfile) PrintOptions(heightMM int, cut bool) ([]string, error) {
	if err := p.SupportsReceiptPrinting(); err != nil {
		return nil, err
	}
	args := []string{"-o", "job-sheets=none", "-o", "Resolution=" + verifiedResolution}
	if p.Has("TmtSpeed", "Auto") {
		args = append(args, "-o", "TmtSpeed=Auto")
	}
	if p.Has("TmtPaperReduction", "Both") {
		args = append(args, "-o", "TmtPaperReduction=Both")
	}
	source := "DocFeedNoCut"
	if cut {
		source = "DocFeedCut"
	}
	args = append(args, "-o", paperSourceOption+"="+source)
	// Odoo's cash-drawer <pulse/> is sent through the direct ESC/POS path.
	for _, option := range []string{"TmtDrawer1", "TmtDrawer2"} {
		if p.Has(option, "Off") {
			args = append(args, "-o", option+"=Off")
		}
	}

	media, err := p.mediaFor(heightMM)
	if err != nil {
		return nil, err
	}
	return append(args, "-o", "media="+media), nil
}

func (p *QueueProfile) mediaFor(heightMM int) (string, error) {
	wantCustom := heightMM > 287
	if !wantCustom && p.hasMedia(standardMedia) {
		return standardMedia, nil
	}
	if p.customMedia() {
		length := heightMM + 10
		if length < 100 {
			length = 100
		}
		return fmt.Sprintf("Custom.80x%dmm", length), nil
	}
	if wantCustom {
		return "", fmt.Errorf("CUPS queue %q has no custom 80 mm media; receipts longer than 297 mm would be cropped (options: %s)",
			p.Queue, p.choiceList(mediaOption))
	}
	return standardMedia, nil
}

// ValidateQueue prevents routing a receipt to a different local system queue
// or to a print server, and rejects queues whose driver profile cannot print
// the verified 80 mm receipt.
func ValidateQueue(ctx context.Context, ip, queue string) error {
	_, err := VerifiedQueue(ctx, ip, queue)
	return err
}

// VerifiedQueue returns the inspected profile after checking both the device
// destination and the driver options.
func VerifiedQueue(ctx context.Context, ip, queue string) (*QueueProfile, error) {
	if !safeQueue.MatchString(queue) {
		return nil, fmt.Errorf("invalid CUPS printer queue name")
	}
	queues, err := MatchingQueues(ctx, ip)
	if err != nil {
		return nil, err
	}
	found := false
	for _, candidate := range queues {
		if candidate == queue {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("CUPS queue %q does not use socket://%s:9100", queue, ip)
	}
	profile, err := InspectQueue(ctx, queue)
	if err != nil {
		return nil, err
	}
	if err := profile.SupportsReceiptPrinting(); err != nil {
		return nil, err
	}
	return profile, nil
}

func writeChunk(out *bytes.Buffer, kind string, payload []byte) {
	_ = binary.Write(out, binary.BigEndian, uint32(len(payload)))
	out.WriteString(kind)
	out.Write(payload)
	checksum := crc32.ChecksumIEEE(append([]byte(kind), payload...))
	_ = binary.Write(out, binary.BigEndian, checksum)
}

// Encode1BitPNG preserves the exact black/white pixels from the Odoo bitmap.
// For ESC/POS bit=1 means black; for 1-bit grayscale PNG bit=0 means black.
func Encode1BitPNG(img *escpos.CUPSImage, dpi int) ([]byte, error) {
	if img == nil || img.Width <= 0 || img.Width%8 != 0 || img.Height <= 0 || dpi <= 0 {
		return nil, fmt.Errorf("invalid monochrome receipt image")
	}
	stride := img.Width / 8
	if stride*img.Height > maxPrintedHeight || len(img.Bitmap) < stride*img.Height {
		return nil, fmt.Errorf("invalid monochrome receipt image")
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	// Per-scanline PNG filter byte 0, followed by the 1-bit inverted raster.
	line := make([]byte, stride+1)
	for row := 0; row < img.Height; row++ {
		offset := row * stride
		for x := 0; x < stride; x++ {
			line[x+1] = ^img.Bitmap[offset+x]
		}
		if _, err := zw.Write(line); err != nil {
			return nil, fmt.Errorf("PNG deflate error: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Write([]byte("\x89PNG\r\n\x1a\n"))
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(img.Width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(img.Height))
	ihdr[8] = 1 // bit depth 1
	ihdr[9] = 0 // grayscale
	writeChunk(&out, "IHDR", ihdr[:])
	var phys [9]byte
	ppm := uint32(float64(dpi)/0.0254 + 0.5)
	binary.BigEndian.PutUint32(phys[0:4], ppm)
	binary.BigEndian.PutUint32(phys[4:8], ppm)
	phys[8] = 1
	writeChunk(&out, "pHYs", phys[:])
	writeChunk(&out, "IDAT", compressed.Bytes())
	writeChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}

// Submit prints a single, unmodified 1-bit ticket via the existing Epson
// CUPS driver. A successful lp command means accepted by CUPS, not that paper
// physically exited. Nothing is automatically resent after lp acceptance.
//
// The submission is detached from HTTP cancellation: if Odoo aborts at 15 s
// after the job was already handed to CUPS, killing `lp` midway could leave a
// half-submitted job while Odoo offers a retry, printing the receipt twice.
func Submit(ctx context.Context, ip, queue string, img *escpos.CUPSImage) (int, error) {
	// Cancellation is honoured while the queue is inspected, the PNG is
	// encoded and the temporary file is prepared. The commitment boundary is
	// immediately before lp is started: after that the job is detached from
	// HTTP cancellation and finishes exactly once.
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	profile, err := VerifiedQueue(ctx, ip, queue)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	pngData, err := Encode1BitPNG(img, defaultDPI)
	if err != nil {
		return 0, err
	}
	heightMM := (img.Height*254 + defaultDPI*10 - 1) / (defaultDPI * 10) // ceil(px*25.4/dpi)
	options, err := profile.PrintOptions(heightMM, img.Cut)
	if err != nil {
		return 0, err
	}

	file, err := os.CreateTemp("", "epos-proxy-receipt-*.png")
	if err != nil {
		return 0, fmt.Errorf("cannot create temporary CUPS receipt: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(pngData); err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("cannot write CUPS receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return 0, err
	}

	args := append([]string{"-d", queue}, options...)
	args = append(args, path)

	// Last check before the commitment point: a cancelled request must not
	// produce an orphan lp job, even with a warm validation cache.
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	submitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cupsTimeout)
	defer cancel()
	if _, err := submitLP(submitCtx, args); err != nil {
		return 0, fmt.Errorf("Epson CUPS queue %q rejected job: %w", queue, err)
	}
	return len(pngData), nil
}

type cacheEntry[T any] struct {
	value T
	at    time.Time
}

var (
	cacheMu          sync.Mutex
	cachedQueueList  = map[string]cacheEntry[[]string]{}
	cachedProfiles   = map[string]cacheEntry[*QueueProfile]{}
	validationMaxAge = validationTTL
)

func cached[T any](cache map[string]cacheEntry[T], key string) (T, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	entry, ok := cache[key]
	var zero T
	if !ok || time.Since(entry.at) > validationMaxAge {
		return zero, false
	}
	return entry.value, true
}

func storeCached[T any](cache map[string]cacheEntry[T], key string, value T) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[key] = cacheEntry[T]{value: value, at: time.Now()}
}

// ForgetQueue drops cached validation, for example after the user changes or
// reinstalls the macOS queue.
func ForgetQueue(queue string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(cachedProfiles, queue)
	cachedQueueList = map[string]cacheEntry[[]string]{}
}
