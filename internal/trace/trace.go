// Package trace records one structured timing trace per print ticket.
//
// The trace is passive: it never changes the bytes sent to the printer, the
// order of device operations, or the response the proxy returns. It only
// records durations and counters the proxy already observes while handling a
// request, then renders them as a single machine-parseable log line:
//
//	print_trace request_id=... kind=receipt printer_id=... transport=lan ...
//
// Timing stages:
//
//	server:    body, parse, enqueue, total
//	printer:   open, write (per block), close
//	CUPS:      lp, drain (reserved; only set by builds that submit via lp)
//
// Nothing in the line carries receipt content, credentials or device paths.
package trace

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"epos-proxy/internal/logger"
)

// Transport values reported in the trace line.
const (
	TransportUSB = "usb"
	TransportLAN = "lan"
)

// Kind values reported in the trace line.
const (
	KindReceipt = "receipt"
	KindLabel   = "label"
)

// Image modes reported in the trace line.
//
//   - none:   the ticket carries no raster image.
//   - legacy: the image was emitted as GS v 0.
//   - gs_l:   the image was emitted as Epson GS ( L graphics.
//   - raw:    the bytes are forwarded verbatim and never rasterised (labels).
const (
	ImageModeNone   = "none"
	ImageModeLegacy = "legacy"
	ImageModeGSL    = "gs_l"
	ImageModeRaw    = "raw"
)

// SettleTimeout bounds how long the HTTP handler waits for the printer loop to
// finish its per-ticket device close before the trace line is written.
//
// The close itself is measured after the job result is handed back, so the wait
// only covers the close decision, which is normally microseconds: a ticket that
// drained the queue closes the device, a ticket that left work queued reports
// close_done=false immediately. The bound exists so a stuck close cannot block
// the handler; the response has already been written when the wait starts.
const SettleTimeout = 250 * time.Millisecond

const (
	maxRequestIDLen  = 64
	maxPrinterIDLen  = 128
	unknownField     = "-"
	statusOK         = "ok"
	statusError      = "error"
	traceLinePrefix  = "print_trace"
	requestIDEntropy = 8
)

var (
	gsLSequence  = []byte{0x1D, 0x28, 0x4C} // GS ( L – Epson graphics
	gsV0Sequence = []byte{0x1D, 0x76, 0x30} // GS v 0 – raster bit image
)

var fallbackID atomic.Uint64

// Trace accumulates the timing stages of a single print ticket. All methods
// are safe for concurrent use: the HTTP handler and the printer loop update
// different fields of the same trace.
type Trace struct {
	mu sync.Mutex

	id        string
	kind      string
	printerID string
	transport string
	imageMode string

	recvAt time.Time

	bodyDur    time.Duration
	parseDur   time.Duration
	enqueueAt  time.Time
	startAt    time.Time
	openDur    time.Duration
	openReused bool
	writeDur   time.Duration
	blocks     []time.Duration
	closeDur   time.Duration
	closeDone  bool
	cupsLPDur  time.Duration
	cupsDrain  time.Duration
	totalDur   time.Duration

	xmlBytes    int
	escposBytes int
	deviceBytes int
	imageCount  int
	rasterW     int
	rasterH     int

	queued bool
	code   string
	err    error

	done     chan struct{}
	doneOnce sync.Once
	logged   bool
}

// New starts a trace for a ticket received at the time of the call.
func New(requestID, printerID, transport, kind string) *Trace {
	return &Trace{
		id:        sanitize(requestID, maxRequestIDLen),
		kind:      kind,
		printerID: sanitize(printerID, maxPrinterIDLen),
		transport: transport,
		imageMode: ImageModeNone,
		recvAt:    time.Now(),
		bodyDur:   -1,
		parseDur:  -1,
		openDur:   -1,
		writeDur:  -1,
		closeDur:  -1,
		cupsLPDur: -1,
		cupsDrain: -1,
		totalDur:  -1,
		done:      make(chan struct{}),
	}
}

// ID returns the request identifier used to correlate the ticket with the
// client, the proxy request log and the printer stages.
func (t *Trace) ID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id
}

// RecvAt reports when the ticket entered the handler.
func (t *Trace) RecvAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recvAt
}

// SetBody records the time spent reading the request body and its length.
func (t *Trace) SetBody(d time.Duration, bytes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bodyDur = d
	t.xmlBytes = bytes
}

// SetParse records the time spent turning the ePOS XML into ESC/POS.
func (t *Trace) SetParse(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parseDur = d
}

// SetImageMode records the image mode detected in the generated ESC/POS.
func (t *Trace) SetImageMode(mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.imageMode = mode
}

// SetRaster records the raster dimensions requested by the ticket.
func (t *Trace) SetRaster(width, height, count int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rasterW = width
	t.rasterH = height
	t.imageCount = count
}

// MarkEnqueue records when the handler handed the job to the printer queue.
func (t *Trace) MarkEnqueue() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enqueueAt = time.Now()
}

// MarkQueued records that the job was accepted by the printer queue.
func (t *Trace) MarkQueued() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queued = true
}

// MarkJobStart records when the printer loop started executing the job.
func (t *Trace) MarkJobStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.startAt = time.Now()
}

// MarkOpen records the time spent opening (or reusing) the device connection.
func (t *Trace) MarkOpen(d time.Duration, reused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.openDur = d
	t.openReused = reused
}

// AddWriteBlock records the duration of one write block.
func (t *Trace) AddWriteBlock(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocks = append(t.blocks, d)
}

// SetWrite records the total write duration and the byte counters. deviceBytes
// is what the device accepted; escposBytes is what the job contained.
func (t *Trace) SetWrite(d time.Duration, escposBytes, deviceBytes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeDur = d
	t.escposBytes = escposBytes
	t.deviceBytes = deviceBytes
}

// AddClose accumulates a close performed as part of error handling.
func (t *Trace) AddClose(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closeDur < 0 {
		t.closeDur = d
		return
	}
	t.closeDur += d
}

// MarkIdleClose records the close performed when the queue drained.
func (t *Trace) MarkIdleClose(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closeDur < 0 {
		t.closeDur = 0
	}
	t.closeDur += d
	t.closeDone = true
}

// SetCUPS records the time spent in lp and, when measured, the time until the
// queue reported the job as gone. It is only used by builds that print through
// CUPS; the field is rendered as "-" otherwise.
func (t *Trace) SetCUPS(lp, drain time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cupsLPDur = lp
	t.cupsDrain = drain
}

// SetCode records the ePOS error code returned to the client.
func (t *Trace) SetCode(code string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.code = code
}

// SetError marks the ticket as failed.
func (t *Trace) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		return
	}
	if t.err == nil {
		t.err = err
	}
}

// SetTotal records the time from handler entry to the serialised response.
func (t *Trace) SetTotal(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalDur = d
}

// Finish signals that the printer loop fully handled the ticket, including the
// device close. It must be called exactly once per traced job.
func (t *Trace) Finish() {
	t.doneOnce.Do(func() { close(t.done) })
}

// Settle waits up to timeout for the printer loop to finish the ticket. The
// wait only happens after the response was sent, so it never delays the client.
func (t *Trace) Settle(timeout time.Duration) {
	t.mu.Lock()
	queued := t.queued
	t.mu.Unlock()
	if !queued {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
	}
}

// Log emits the trace line once. Repeated calls are ignored so a handler that
// takes several paths cannot print the same ticket twice.
func (t *Trace) Log() {
	t.mu.Lock()
	if t.logged {
		t.mu.Unlock()
		return
	}
	t.logged = true
	line := t.lineLocked()
	t.mu.Unlock()
	logger.Info(line)
}

// Line renders the trace line without logging it. It is intended for tests and
// for callers that want to route the line somewhere else.
func (t *Trace) Line() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lineLocked()
}

func (t *Trace) lineLocked() string {
	status := statusOK
	if t.err != nil {
		status = statusError
	}

	var b strings.Builder
	b.Grow(640)
	b.WriteString(traceLinePrefix)
	writeField(&b, "request_id", t.id)
	writeField(&b, "kind", t.kind)
	writeField(&b, "printer_id", t.printerID)
	writeField(&b, "transport", t.transport)
	writeField(&b, "image_mode", t.imageMode)
	writeField(&b, "status", status)
	writeField(&b, "code", t.code)
	writeInt(&b, "xml_bytes", t.xmlBytes)
	writeInt(&b, "escpos_bytes", t.escposBytes)
	writeInt(&b, "device_bytes", t.deviceBytes)
	writeInt(&b, "write_blocks", len(t.blocks))
	writeInt(&b, "image_count", t.imageCount)
	writeInt(&b, "raster_w", t.rasterW)
	writeInt(&b, "raster_h", t.rasterH)
	writeField(&b, "recv_at", t.recvAt.UTC().Format(time.RFC3339Nano))
	writeDur(&b, "body_ms", t.bodyDur)
	writeDur(&b, "parse_ms", t.parseDur)
	writeDur(&b, "enqueue_ms", t.enqueueDurLocked())
	writeDur(&b, "open_ms", t.openDur)
	writeField(&b, "open_reused", strconv.FormatBool(t.openReused))
	writeDur(&b, "write_ms", t.writeDur)
	writeField(&b, "block_ms", t.blocksLocked())
	writeDur(&b, "close_ms", t.closeDur)
	writeField(&b, "close_done", strconv.FormatBool(t.closeDone))
	writeDur(&b, "cups_lp_ms", t.cupsLPDur)
	writeDur(&b, "cups_drain_ms", t.cupsDrain)
	writeDur(&b, "total_ms", t.totalDur)
	if t.err != nil {
		writeField(&b, "error", strconv.Quote(oneLine(t.err.Error())))
	}
	return b.String()
}

func (t *Trace) enqueueDurLocked() time.Duration {
	if t.enqueueAt.IsZero() || t.startAt.IsZero() {
		return -1
	}
	return t.startAt.Sub(t.enqueueAt)
}

func (t *Trace) blocksLocked() string {
	if len(t.blocks) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range t.blocks {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(formatMillis(d))
	}
	return b.String()
}

// RequestID returns the client-supplied X-Request-Id when it is usable, or a
// fresh random identifier otherwise. The value is sanitised so it can be
// embedded in a log line without escaping.
func RequestID(clientID string) string {
	if id := sanitize(clientID, maxRequestIDLen); hasAlphanumeric(id) {
		return id
	}
	var raw [requestIDEntropy]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return "req-" + strconv.FormatUint(fallbackID.Add(1), 10)
}

// hasAlphanumeric rejects client values that sanitising turned into
// punctuation only, since those would collide across tickets.
func hasAlphanumeric(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// DetectImageMode inspects the generated ESC/POS stream and reports which
// raster command carries the ticket image. Detecting the mode from the bytes
// rather than from configuration keeps the trace correct when a new image mode
// is added: the trace follows the bytes that are actually sent.
func DetectImageMode(jobData []byte) string {
	if bytes.Contains(jobData, gsLSequence) {
		return ImageModeGSL
	}
	if bytes.Contains(jobData, gsV0Sequence) {
		return ImageModeLegacy
	}
	return ImageModeNone
}

// RasterSize reports the raster dimensions requested by the ePOS XML: the
// widest image and the summed height of every image, in dots.
//
// It reads only the <image> start tags, so its cost is proportional to the
// number of tags rather than to the base64 payload. The scan is best-effort:
// malformed bodies simply report no images and the XML parser rejects them
// later.
func RasterSize(body []byte) (width, height, count int) {
	rest := body
	for {
		start := indexTagName(rest, "image")
		if start < 0 {
			return width, height, count
		}
		rest = rest[start:]
		end := bytes.IndexByte(rest, '>')
		if end < 0 {
			return width, height, count
		}
		tag := rest[:end]
		rest = rest[end+1:]

		count++
		if w := tagAttrInt(tag, "width"); w > width {
			width = w
		}
		height += tagAttrInt(tag, "height")
	}
}

// indexTagName reports the offset of the next opening tag whose name matches
// case-insensitively, mirroring how the XML parser normalises element names.
func indexTagName(body []byte, name string) int {
	offset := 0
	for offset < len(body) {
		i := bytes.IndexByte(body[offset:], '<')
		if i < 0 {
			return -1
		}
		i += offset
		end := i + 1 + len(name)
		if end <= len(body) && strings.EqualFold(string(body[i+1:end]), name) {
			if end == len(body) || isNameDelimiter(body[end]) {
				return i
			}
		}
		offset = i + 1
	}
	return -1
}

func isNameDelimiter(b byte) bool {
	switch b {
	case '>', '/', ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

// tagAttrInt returns the integer value of a start-tag attribute, matching
// attribute names case-insensitively like the XML parser does.
func tagAttrInt(tag []byte, name string) int {
	lower := bytes.ToLower(tag)
	needle := []byte(name)
	offset := 0
	for {
		i := bytes.Index(lower[offset:], needle)
		if i < 0 {
			return 0
		}
		i += offset
		after := i + len(needle)
		if after >= len(tag) {
			return 0
		}
		if lower[after] != '=' {
			offset = after
			continue
		}
		value := tag[after+1:]
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			quote := value[0]
			value = value[1:]
			if closing := bytes.IndexByte(value, quote); closing >= 0 {
				value = value[:closing]
			}
		} else if space := bytes.IndexAny(value, " \t\r\n>"); space >= 0 {
			value = value[:space]
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(value)))
		if err != nil {
			return 0
		}
		return n
	}
}

func writeField(b *strings.Builder, key, value string) {
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteByte('=')
	if value == "" {
		b.WriteString(unknownField)
		return
	}
	b.WriteString(value)
}

func writeInt(b *strings.Builder, key string, value int) {
	writeField(b, key, strconv.Itoa(value))
}

func writeDur(b *strings.Builder, key string, d time.Duration) {
	if d < 0 {
		writeField(b, key, "")
		return
	}
	writeField(b, key, formatMillis(d))
}

func formatMillis(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)
}

func sanitize(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.', r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[:maxLen]
	}
	return out
}

// oneLine keeps quoted error values on a single log line.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
