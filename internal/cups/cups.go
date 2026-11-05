// Package cups submits Epson receipts through a locally installed CUPS
// queue. It does not use raw mode: the Epson printer driver must process the
// 1-bit PNG, just as it does when printing through the macOS print dialog.
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
	"strconv"
	"strings"
	"time"

	"epos-proxy/internal/escpos"
)

const (
	cupsTimeout      = 25 * time.Second
	lpstatTimeout    = 5 * time.Second
	defaultDPI       = 180 // Verified Epson TM-T88IV TMTIENDA queue on macOS
	maxPrintedHeight = 4 * 1024 * 1024
)

var safeQueue = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,127}$`)

// MatchingQueues returns installed local CUPS queues using socket://IP:9100.
// It never silently chooses a printer with an unrelated device address.
func MatchingQueues(ctx context.Context, ip string) ([]string, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("local Epson CUPS mode is only enabled on macOS")
	}
	c, cancel := context.WithTimeout(ctx, lpstatTimeout)
	defer cancel()
	out, err := exec.CommandContext(c, "lpstat", "-v").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cannot list CUPS queues: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseMatchingQueues(string(out), ip), nil
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

// ValidateQueue prevents routing a receipt to a different local system queue
// or to a print server. The queue must be backed by the same LAN IP as obox-app.
func ValidateQueue(ctx context.Context, ip, queue string) error {
	if !safeQueue.MatchString(queue) {
		return fmt.Errorf("invalid CUPS printer queue name")
	}
	queues, err := MatchingQueues(ctx, ip)
	if err != nil {
		return err
	}
	for _, found := range queues {
		if found == queue {
			return nil
		}
	}
	return fmt.Errorf("CUPS queue %q does not use socket://%s:9100", queue, ip)
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
	if img == nil || img.Width <= 0 || img.Width%8 != 0 || img.Height <= 0 ||
		img.Width/8 > maxPrintedHeight || img.Height > maxPrintedHeight/(img.Width/8) ||
		len(img.Bitmap) < (img.Width/8)*img.Height || dpi <= 0 {
		return nil, fmt.Errorf("invalid monochrome receipt image")
	}
	stride := img.Width / 8
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
func Submit(ctx context.Context, ip, queue string, img *escpos.CUPSImage) (int, error) {
	if err := ValidateQueue(ctx, ip, queue); err != nil {
		return 0, err
	}
	pngData, err := Encode1BitPNG(img, defaultDPI)
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

	source := "DocFeedNoCut"
	if img.Cut {
		source = "DocFeedCut"
	}
	args := []string{
		"-d", queue,
		"-o", "job-sheets=none",
		"-o", "Resolution=180x180dpi",
		"-o", "TmtSpeed=Auto",
		"-o", "TmtPaperReduction=Both",
		"-o", "TmtPaperSource=" + source,
		// Odoo's cash-drawer <pulse/> is sent through the direct ESC/POS path.
		"-o", "TmtDrawer1=Off",
		"-o", "TmtDrawer2=Off",
		"-o", "ppi=" + strconv.Itoa(defaultDPI),
	}
	// The verified queue defaults to 80x297 mm. For long Odoo receipts,
	// request a custom sheet length to avoid cropping at 297 mm.
	heightMM := (img.Height*254 + defaultDPI*10 - 1) / (defaultDPI*10) // ceil(px*25.4/dpi)
	if heightMM > 287 {
		args = append(args, "-o", fmt.Sprintf("media=Custom.80x%dmm", heightMM+10))
	} else {
		args = append(args, "-o", "media=RP80x297")
	}
	args = append(args, path)

	c, cancel := context.WithTimeout(ctx, cupsTimeout)
	defer cancel()
	output, err := exec.CommandContext(c, "lp", args...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("Epson CUPS queue %q rejected job: %w: %s", queue, err, strings.TrimSpace(string(output)))
	}
	return len(pngData), nil
}
