package printer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"epos-proxy/internal/testutil"
	"epos-proxy/internal/trace"
)

// recordingConn is an in-memory LAN device. Tests use it instead of binding the
// fixed LAN port, so this package does not compete with the server package when
// `go test ./...` runs both in parallel.
type recordingConn struct {
	mu     sync.Mutex
	writes int
	bytes  []byte
}

func (c *recordingConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes++
	c.bytes = append(c.bytes, p...)
	return len(p), nil
}
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingConn) received() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bytes)
}

func (c *recordingConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

// registerExistingPrinter puts a printer under the manager cache without
// dialling, so the test can install an in-memory connection.
func registerExistingPrinter(mgr *Manager, printerID string, p *Printer) {
	mgr.printers[printerID] = p
	go p.loop()
}

func TestWriteAsyncContextSkipsCanceledJobBeforeDeviceWrite(t *testing.T) {
	mgr := NewManager()
	printerID := EncodeLANPrinterID("127.0.0.1")
	conn := &recordingConn{}
	registerExistingPrinter(mgr, printerID, &Printer{
		connectionType: ConnKindLAN,
		lanIP:          "127.0.0.1",
		tcpConn:        conn,
		jobs:           make(chan Job, QueueSize),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := trace.New("req-canceled", printerID, trace.TransportLAN, trace.KindReceipt)
	reply, err := mgr.WriteAsyncContext(ctx, printerID, []byte("MUST NOT PRINT"), tr)
	testutil.ExpectedTrue(t, errors.Is(err, ErrCanceled), "expected ErrCanceled, got "+fmt.Sprint(err))
	testutil.ExpectedTrue(t, reply == nil, "a request canceled before queueing must not return a reply channel")

	testutil.ExpectedEqual(t, conn.writeCount(), 0)
	testutil.ExpectedLen(t, conn.received(), 0)

	line := tr.Line()
	testutil.ExpectedContains(t, line, "client_canceled=true")
	testutil.ExpectedContains(t, line, "cancel_stage="+trace.CancelStageBeforeEnqueue)
	testutil.ExpectedTrue(t, tr.ClientCanceled(), "canceled job must be visible in the trace")
}

func TestWriteAsyncContextSkipsJobCanceledWhileQueued(t *testing.T) {
	mgr := NewManager()
	printerID := EncodeLANPrinterID("127.0.0.1")
	conn := &recordingConn{}
	p := &Printer{connectionType: ConnKindLAN, lanIP: "127.0.0.1", tcpConn: conn, jobs: make(chan Job, QueueSize)}
	registerExistingPrinter(mgr, printerID, p)
	t.Cleanup(func() { p.close() })

	release := make(chan struct{})
	started := make(chan struct{})
	testutil.ExpectedNoError(t, p.Enqueue(func(*Printer) JobResult {
		close(started)
		<-release
		return JobResult{OK: true}
	}, nil))
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	tr := trace.New("req-queued", printerID, trace.TransportLAN, trace.KindReceipt)
	reply, err := mgr.WriteAsyncContext(ctx, printerID, []byte("MUST NOT PRINT"), tr)
	testutil.ExpectedNoError(t, err)

	cancel()
	close(release)

	select {
	case result := <-reply:
		testutil.ExpectedTrue(t, errors.Is(result.Err, ErrCanceled), "expected ErrCanceled, got "+fmt.Sprint(result.Err))
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the queued job reply")
	}

	testutil.ExpectedEqual(t, conn.writeCount(), 0)
	testutil.ExpectedLen(t, conn.received(), 0)
	testutil.ExpectedContains(t, tr.Line(), "cancel_stage="+trace.CancelStageQueued)
}

func TestWriteCompletesWhenCancellationArrivesDuringDeviceWrite(t *testing.T) {
	deviceSide, proxySide := net.Pipe()
	defer deviceSide.Close()

	mgr := NewManager()
	printerID := EncodeLANPrinterID("127.0.0.1")
	p := &Printer{connectionType: ConnKindLAN, lanIP: "127.0.0.1", tcpConn: proxySide, jobs: make(chan Job, QueueSize)}
	registerExistingPrinter(mgr, printerID, p)
	t.Cleanup(func() { p.close() })

	payload := bytes.Repeat([]byte("TICKET"), 64*1024)
	ctx, cancel := context.WithCancel(context.Background())
	tr := trace.New("req-mid-write", printerID, trace.TransportLAN, trace.KindReceipt)

	reply, err := mgr.WriteAsyncContext(ctx, printerID, payload, tr)
	testutil.ExpectedNoError(t, err)

	// The write blocks on the pipe, so open_ms proves it already started.
	deadline := time.Now().Add(2 * time.Second)
	for strings.Contains(tr.Line(), "open_ms=-") {
		if time.Now().After(deadline) {
			t.Fatalf("device write never started: %s", tr.Line())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	got, err := io.ReadAll(deviceSide)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedBytesEqual(t, got, payload, "a write already sent must complete in full")

	select {
	case result := <-reply:
		testutil.ExpectedTrue(t, result.OK, "cancellation must not turn a sent ticket into a failure: "+fmt.Sprint(result.Err))
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the mid-write job reply")
	}

	line := tr.Line()
	testutil.ExpectedContains(t, line, "device_bytes="+strconv.Itoa(len(payload)))
	testutil.ExpectedFalse(t, tr.ClientCanceled(), "the printer path must not mark a write it completed as canceled")
}

type countingConn struct {
	writes atomic.Int32
}

func (c *countingConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *countingConn) Write([]byte) (int, error) {
	c.writes.Add(1)
	return 0, errors.New("device write failed")
}
func (c *countingConn) Close() error                     { return nil }
func (c *countingConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (c *countingConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (c *countingConn) SetDeadline(time.Time) error      { return nil }
func (c *countingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countingConn) SetWriteDeadline(time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "tcp" }
func (fakeAddr) String() string  { return "fake" }

func TestWriteFailureIsNotRetried(t *testing.T) {
	conn := &countingConn{}
	mgr := NewManager()
	printerID := EncodeLANPrinterID("127.0.0.1")
	p := &Printer{connectionType: ConnKindLAN, lanIP: "127.0.0.1", tcpConn: conn, jobs: make(chan Job, QueueSize)}
	registerExistingPrinter(mgr, printerID, p)
	t.Cleanup(func() { p.close() })

	reply, err := mgr.WriteAsyncContext(context.Background(), printerID, []byte("TICKET"), nil)
	testutil.ExpectedNoError(t, err)

	select {
	case result := <-reply:
		testutil.ExpectedFalse(t, result.OK)
		testutil.ExpectedTrue(t, result.Err != nil, "a failed write must report an error")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the failed job reply")
	}

	testutil.ExpectedEqual(t, int(conn.writes.Load()), 1)
	testutil.ExpectedEqual(t, len(p.jobs), 0)
}
