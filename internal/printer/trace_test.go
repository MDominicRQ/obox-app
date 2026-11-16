package printer

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"epos-proxy/internal/testutil"
	"epos-proxy/internal/trace"
)

// captureLANWrites writes each payload to a mock LAN printer and returns the
// exact bytes the printer received, in order.
func captureLANWrites(t *testing.T, traces []*trace.Trace, payloads ...[]byte) [][]byte {
	t.Helper()

	received := make(chan []byte, len(payloads))
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		payload, _ := io.ReadAll(conn)
		received <- payload
	})
	testutil.ExpectedNoError(t, err)

	for i, payload := range payloads {
		p := &Printer{connectionType: ConnKindLAN, lanIP: "127.0.0.1"}
		testutil.ExpectedNoError(t, p.write(payload, traces[i]))
		p.close()
	}

	captured := make([][]byte, 0, len(payloads))
	for range payloads {
		select {
		case payload := <-received:
			captured = append(captured, payload)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for the mock printer payload")
		}
	}
	return captured
}

func TestWriteTracedKeepsDeviceBytesIdentical(t *testing.T) {
	payload := []byte{0x1B, 0x40, 'O', 'R', 'D', 'E', 'R', ' ', '#', '1', 0x1D, 0x56, 0x41, 0x0A}
	tr := trace.New("req-plain", "LAN:127.0.0.1", trace.TransportLAN, trace.KindReceipt)

	captured := captureLANWrites(t, []*trace.Trace{nil, tr}, payload, payload)

	testutil.ExpectedBytesEqual(t, captured[0], payload)
	testutil.ExpectedBytesEqual(t, captured[1], captured[0])
}

func TestWriteTracedRecordsDeviceStages(t *testing.T) {
	payload := []byte("ESC/POS INSTRUMENTED RECEIPT")
	tr := trace.New("req-stages", "LAN:127.0.0.1", trace.TransportLAN, trace.KindReceipt)

	captured := captureLANWrites(t, []*trace.Trace{tr}, payload)
	testutil.ExpectedBytesEqual(t, captured[0], payload)

	line := tr.Line()
	for _, want := range []string{
		"transport=lan",
		"escpos_bytes=" + strconv.Itoa(len(payload)),
		"device_bytes=" + strconv.Itoa(len(payload)),
		"write_blocks=1",
		"open_reused=false",
		"write_ms=",
		"open_ms=",
	} {
		testutil.ExpectedContains(t, line, want)
	}
	if strings.Contains(line, "write_ms=-") {
		t.Errorf("write_ms must be measured for a completed write: %s", line)
	}
	if strings.Contains(line, "open_ms=-") {
		t.Errorf("open_ms must be measured for a completed open: %s", line)
	}
}

func TestManagerWriteAsyncTracedReportsClose(t *testing.T) {
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = io.ReadAll(conn)
	})
	testutil.ExpectedNoError(t, err)

	mgr := NewManager()
	printerID := EncodeLANPrinterID("127.0.0.1")
	tr := trace.New("req-manager", printerID, trace.TransportLAN, trace.KindReceipt)

	reply, err := mgr.WriteAsyncTraced(printerID, []byte("MANAGER TRACED JOB"), tr)
	testutil.ExpectedNoError(t, err)

	select {
	case result := <-reply:
		testutil.ExpectedNoError(t, result.Err)
		testutil.ExpectedTrue(t, result.OK, "job must succeed")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the print job reply")
	}

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(tr.Line(), "close_done=true") {
		if time.Now().After(deadline) {
			t.Fatalf("printer loop never reported the close: %s", tr.Line())
		}
		time.Sleep(5 * time.Millisecond)
	}

	line := tr.Line()
	testutil.ExpectedContains(t, line, "kind=receipt")
	testutil.ExpectedContains(t, line, "write_blocks=1")
	testutil.ExpectedContains(t, line, "close_ms=")
	if strings.Contains(line, "enqueue_ms=-") {
		t.Errorf("enqueue_ms must be measured once the job ran: %s", line)
	}
}
