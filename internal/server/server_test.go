package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"epos-proxy/internal/certs"
	"epos-proxy/internal/escpos"
	"epos-proxy/internal/printer"
	"epos-proxy/internal/testutil"
	"epos-proxy/internal/trace"
)

func testResponse(s *Server, req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	s.app.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestServer_Lifecycle(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	testutil.ExpectedTrue(t, s.Running(), "Expected server to be running after New()")
	testutil.ExpectedEqual(t, s.Port, port)

	err := s.Stop()
	testutil.ExpectedNoError(t, err)
}

func TestPrintData_ValidXML_Success(t *testing.T) {
	// Start mock TCP listener on port 9100 for LAN printer
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
	})
	testutil.ExpectedNoError(t, err)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	xmlPayload := `<epos-print><text align="center">ORDER #123</text><cut /></epos-print>`

	url := fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID)
	req := httptest.NewRequest("POST", url, bytes.NewReader([]byte(xmlPayload)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)

	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(body), `success="true"`)
}

func TestPrintData_SchemaError(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	invalidPayload := `<invalid>not-an-epos-print</invalid>`
	req := httptest.NewRequest("POST", "/p/any-printer/cgi-bin/epos/service.cgi", bytes.NewReader([]byte(invalidPayload)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	testutil.ExpectedContains(t, bodyStr, `success="false"`)
	testutil.ExpectedContains(t, bodyStr, `code="SchemaError"`)
}

func TestPrintData_UnreachablePrinter_EX_BADPORT(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	// Use a non-existent USB printer serial that cannot be found
	printerID := "czpOT05fRVhJU1RFTlRfU0VSSUFMCg"
	xmlPayload := `<epos-print><text>Hello</text></epos-print>`

	url := fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID)
	req := httptest.NewRequest("POST", url, bytes.NewReader([]byte(xmlPayload)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	testutil.ExpectedContains(t, bodyStr, `success="false"`)
	testutil.ExpectedContains(t, bodyStr, `code="EX_BADPORT"`)
}

func TestPrintLabel_Success(t *testing.T) {
	// Start mock TCP listener on port 9100
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
	})
	testutil.ExpectedNoError(t, err)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	labelData := []byte("^XA^FDBarcode123^FS^XZ")

	url := fmt.Sprintf("/p/%s/pstprnt", printerID)
	req := httptest.NewRequest("POST", url, bytes.NewReader(labelData))

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)
}

func TestPrintLabel_EmptyBody_BadRequest(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	req := httptest.NewRequest("POST", "/p/any-printer/pstprnt", bytes.NewReader([]byte{}))
	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusBadRequest)
}

func TestPrintLabel_UnreachablePrinter_ServerError(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := "czpOT05fRVhJU1RFTlRfU0VSSUFMCg"
	labelData := []byte("^XA^FDTest^FS^XZ")

	url := fmt.Sprintf("/p/%s/pstprnt", printerID)
	req := httptest.NewRequest("POST", url, bytes.NewReader(labelData))

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusInternalServerError)
}

func TestCORSHeaders(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	req := httptest.NewRequest("OPTIONS", "/cgi-bin/epos/service.cgi", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)

	allowOrigin := resp.Header.Get("Access-Control-Allow-Origin")
	testutil.ExpectedEqual(t, allowOrigin, "http://example.com")
}

func TestPrintData_AutoSelectRoute(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		expected []string
	}{
		{
			name:    "schema error",
			payload: `<bad></bad>`,
			expected: []string{
				`code="SchemaError"`,
			},
		},
		{
			name:    "no USB printer",
			payload: `<epos-print><text align="center">RECEIPT</text><cut /></epos-print>`,
			expected: []string{
				`success="false"`,
				`code="EX_BADPORT"`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			port := testutil.GetFreePort(t)
			mgr := printer.NewManager()
			s := New(port, mgr)
			defer s.Stop()

			req := httptest.NewRequest("POST", "/cgi-bin/epos/service.cgi", bytes.NewReader([]byte(tc.payload)))
			req.Header.Set("Content-Type", "text/xml")

			resp, err := testResponse(s, req)
			testutil.ExpectedNoError(t, err)

			body, err := io.ReadAll(resp.Body)
			testutil.ExpectedNoError(t, err)

			for _, expected := range tc.expected {
				testutil.ExpectedContains(t, string(body), expected)
			}
		})
	}
}

func TestPrinterBaseRouteIsBrowserTestable(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	for _, path := range []string{"/", "/healthz", "/p/test-printer", "/p/test-printer/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp, err := testResponse(s, req)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedContains(t, string(body), `"status":"ok"`)
	}

	req := httptest.NewRequest(http.MethodGet, "/p/test-printer", nil)
	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(body), `"printerId":"test-printer"`)
	testutil.ExpectedContains(t, string(body), "ePOS Proxy printer endpoint is reachable")
}

func TestEPOSConnectivityCheckRoutes(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	for _, path := range []string{
		"/cgi-bin/epos/service.cgi?devid=local_printer",
		"/p/test-printer/cgi-bin/epos/service.cgi?devid=local_printer",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp, err := testResponse(s, req)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedEqual(t, len(body), 0)
	}
}

func TestServer_HTTPAndHTTPSCORSPreflight(t *testing.T) {
	httpPort := testutil.GetFreePort(t)
	httpsPort := testutil.GetFreePort(t)
	for httpsPort == httpPort {
		httpsPort = testutil.GetFreePort(t)
	}

	paths, err := certs.Ensure(t.TempDir(), "127.0.0.1")
	testutil.ExpectedNoError(t, err)

	mgr := printer.NewManager()
	s := NewWithTLS(httpPort, httpsPort, paths.ServerCert, paths.ServerKey, mgr)
	defer s.Stop()

	caPEM, err := os.ReadFile(paths.CACert)
	testutil.ExpectedNoError(t, err)
	pool := x509.NewCertPool()
	testutil.ExpectedTrue(t, pool.AppendCertsFromPEM(caPEM), "Expected CA certificate to be added to pool")

	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    pool,
			},
		},
		Timeout: 2 * time.Second,
	}

	for _, tc := range []struct {
		name   string
		url    string
		client *http.Client
	}{
		{
			name:   "http",
			url:    fmt.Sprintf("http://127.0.0.1:%d/cgi-bin/epos/service.cgi", httpPort),
			client: &http.Client{Timeout: 2 * time.Second},
		},
		{
			name:   "https",
			url:    fmt.Sprintf("https://127.0.0.1:%d/cgi-bin/epos/service.cgi", httpsPort),
			client: httpsClient,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lastErr error
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				req, err := http.NewRequest(http.MethodOptions, tc.url, nil)
				testutil.ExpectedNoError(t, err)
				req.Header.Set("Origin", "https://odoo.example")
				req.Header.Set("Access-Control-Request-Method", "POST")
				req.Header.Set("Access-Control-Request-Private-Network", "true")

				resp, err := tc.client.Do(req)
				if err != nil {
					lastErr = err
					time.Sleep(25 * time.Millisecond)
					continue
				}
				_ = resp.Body.Close()

				testutil.ExpectedEqual(t, resp.Header.Get("Access-Control-Allow-Origin"), "https://odoo.example")
				testutil.ExpectedEqual(t, resp.Header.Get("Access-Control-Allow-Private-Network"), "true")
				return
			}
			t.Fatalf("%s endpoint did not become ready for CORS preflight: %v", tc.name, lastErr)
		})
	}
}

func TestServer_HTTPAndHTTPSEndpoints(t *testing.T) {
	httpPort := testutil.GetFreePort(t)
	httpsPort := testutil.GetFreePort(t)
	for httpsPort == httpPort {
		httpsPort = testutil.GetFreePort(t)
	}

	paths, err := certs.Ensure(t.TempDir(), "127.0.0.1")
	testutil.ExpectedNoError(t, err)

	mgr := printer.NewManager()
	s := NewWithTLS(httpPort, httpsPort, paths.ServerCert, paths.ServerKey, mgr)
	defer s.Stop()

	caPEM, err := os.ReadFile(paths.CACert)
	testutil.ExpectedNoError(t, err)
	pool := x509.NewCertPool()
	testutil.ExpectedTrue(t, pool.AppendCertsFromPEM(caPEM), "Expected CA certificate to be added to pool")

	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    pool,
			},
		},
		Timeout: 2 * time.Second,
	}

	for _, tc := range []struct {
		name   string
		url    string
		client *http.Client
	}{
		{
			name:   "http",
			url:    fmt.Sprintf("http://127.0.0.1:%d/cgi-bin/epos/service.cgi", httpPort),
			client: &http.Client{Timeout: 2 * time.Second},
		},
		{
			name:   "https",
			url:    fmt.Sprintf("https://127.0.0.1:%d/cgi-bin/epos/service.cgi", httpsPort),
			client: httpsClient,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lastErr error
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				req, err := http.NewRequest(http.MethodPost, tc.url, bytes.NewReader([]byte("<invalid />")))
				testutil.ExpectedNoError(t, err)
				req.Header.Set("Content-Type", "text/xml")

				resp, err := tc.client.Do(req)
				if err != nil {
					lastErr = err
					time.Sleep(25 * time.Millisecond)
					continue
				}

				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				testutil.ExpectedNoError(t, readErr)
				testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)
				testutil.ExpectedContains(t, string(body), `code="SchemaError"`)
				return
			}
			t.Fatalf("%s endpoint did not become ready: %v", tc.name, lastErr)
		})
	}
}

func TestServer_NetworkPrintingPolicyBlocksRemoteClients(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	enabled := false
	s := New(port, mgr, AccessPolicy{
		NetworkPrintingEnabled: func() bool { return enabled },
	})
	defer s.Stop()

	remoteReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	remoteReq.RemoteAddr = "192.168.50.20:54321"
	resp, err := testResponse(s, remoteReq)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusForbidden)

	localReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	localReq.RemoteAddr = "127.0.0.1:54321"
	resp, err = testResponse(s, localReq)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)

	enabled = true
	remoteReq = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	remoteReq.RemoteAddr = "192.168.50.20:54321"
	resp, err = testResponse(s, remoteReq)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)
}

func TestPrintData_InvalidImageDimensions_ReturnsSchemaError(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	payload := `<epos-print><image width="-1" height="2">AA==</image></epos-print>`
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/epos/service.cgi", bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(body), `success="false"`)
	testutil.ExpectedContains(t, string(body), `code="SchemaError"`)
}

// completeTicket exercises every element the proxy can rasterise, so the byte
// comparison below covers text, feed, image, cut and pulse.
const completeTicket = `<epos-print>
<text align="center" em="true">ORDER #42</text>
<feed line="2" />
<image width="8" height="2">qlU=</image>
<cut />
<pulse drawer="drawer_1" time="pulse_100" />
</epos-print>`

func startCapturePrinter(t *testing.T) <-chan []byte {
	t.Helper()
	received := make(chan []byte, 1)
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		payload, _ := io.ReadAll(conn)
		received <- payload
	})
	testutil.ExpectedNoError(t, err)
	return received
}

func TestPrintData_DeviceBytesMatchEscposOutput(t *testing.T) {
	received := startCapturePrinter(t)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID), bytes.NewReader([]byte(completeTicket)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(body), `success="true"`)

	expected, err := escpos.ParseXML([]byte(completeTicket))
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedTrue(t, bytes.Contains(expected, escpos.CmdCut), "expected ESC/POS output to contain the cut command")

	select {
	case captured := <-received:
		testutil.ExpectedBytesEqual(t, captured, expected, "instrumentation must not alter the ESC/POS stream")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the mock printer payload")
	}

	if id := resp.Header.Get(requestIDHeader); len(id) != 2*8 {
		t.Errorf("expected a generated request id on the response, got %q", id)
	}
}

func TestPrintData_EchoesClientRequestID(t *testing.T) {
	received := startCapturePrinter(t)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID), bytes.NewReader([]byte(completeTicket)))
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set(requestIDHeader, "bench-short-01")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.Header.Get(requestIDHeader), "bench-short-01")

	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the mock printer payload")
	}
}

func TestPrintData_UnusableRequestIDIsReplaced(t *testing.T) {
	received := startCapturePrinter(t)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID), bytes.NewReader([]byte(completeTicket)))
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set(requestIDHeader, "   ")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	if id := resp.Header.Get(requestIDHeader); id == "" || id == "   " {
		t.Errorf("expected a generated request id, got %q", id)
	}

	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the mock printer payload")
	}
}

func TestPrintTransport(t *testing.T) {
	testutil.ExpectedEqual(t, printTransport(printer.EncodeLANPrinterID("192.168.1.50")), "lan")
	testutil.ExpectedEqual(t, printTransport("czpOT05fRVhJU1RFTlRfU0VSSUFMCg"), "usb")
	testutil.ExpectedEqual(t, printTransport(""), "usb")
}

func TestFinishTraceDoesNotBlockHandler(t *testing.T) {
	tr := trace.New("req-async", "LAN:127.0.0.1", trace.TransportLAN, trace.KindReceipt)
	tr.MarkQueued()

	started := time.Now()
	finishTrace(tr, "", nil)
	elapsed := time.Since(started)

	if elapsed >= 150*time.Millisecond {
		t.Fatalf("finishTrace blocked the handler for %s; it must settle off the request path", elapsed)
	}

	tr.Finish()
}

// TestPrintData_CanceledBeforeEnqueueDoesNotPrint covers the first safety rule:
// a request Odoo already abandoned must not reach the printer, because Odoo
// will offer a retry and that retry would print a duplicate.
func TestPrintData_CanceledBeforeEnqueueDoesNotPrint(t *testing.T) {
	received := startCapturePrinter(t)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID), bytes.NewReader([]byte(completeTicket))).
		WithContext(ctx)
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)

	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedLen(t, body, 0)

	select {
	case payload := <-received:
		t.Fatalf("device received %d bytes for a canceled request: %q", len(payload), payload)
	case <-time.After(250 * time.Millisecond):
	}
}

// TestPrintData_LabelCanceledBeforeEnqueueDoesNotPrint mirrors the receipt
// rule on the label endpoint.
func TestPrintData_LabelCanceledBeforeEnqueueDoesNotPrint(t *testing.T) {
	received := startCapturePrinter(t)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/pstprnt", printerID), bytes.NewReader([]byte("^XA^FDCANCELED^FS^XZ"))).
		WithContext(ctx)

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedLen(t, body, 0)

	select {
	case payload := <-received:
		t.Fatalf("device received %d bytes for a canceled label request: %q", len(payload), payload)
	case <-time.After(250 * time.Millisecond):
	}
}

// TestPrintData_SendsEachTicketOnce pins the single-send contract: one POST
// produces exactly one device write and no automatic retry.
func TestPrintData_SendsEachTicketOnce(t *testing.T) {
	payloads := make(chan []byte, 4)
	var connections atomic.Int32
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		connections.Add(1)
		payload, _ := io.ReadAll(conn)
		if len(payload) > 0 {
			payloads <- payload
		}
	})
	testutil.ExpectedNoError(t, err)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	printerID := printer.EncodeLANPrinterID("127.0.0.1")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", printerID), bytes.NewReader([]byte(completeTicket)))
	req.Header.Set("Content-Type", "text/xml")

	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	body, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(body), `success="true"`)

	expected, err := escpos.ParseXML([]byte(completeTicket))
	testutil.ExpectedNoError(t, err)

	select {
	case captured := <-payloads:
		testutil.ExpectedBytesEqual(t, captured, expected, "the device must receive the ticket exactly once")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the ticket payload")
	}

	select {
	case extra := <-payloads:
		t.Fatalf("device received a second write of %d bytes; the proxy must not resend", len(extra))
	case <-time.After(250 * time.Millisecond):
	}

	testutil.ExpectedEqual(t, int(connections.Load()), 1)
}

// TestCancelStageFor pins the labelling rule: a job that ended in a device
// error must never be reported as if the ticket had been written.
func TestCancelStageFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"dropped before the device", fmt.Errorf("%w: context canceled", printer.ErrCanceled), trace.CancelStageQueued},
		{"device write failed", errors.New("device write failed"), trace.CancelStageAfterWriteFail},
		{"successful write", nil, trace.CancelStageAfterWrite},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cancelStageFor(tc.err)
			testutil.ExpectedEqual(t, got, tc.want)
			if tc.err != nil && got == trace.CancelStageAfterWrite {
				t.Fatalf("a failed job must not be labelled %q", trace.CancelStageAfterWrite)
			}
		})
	}
}
func TestResolveImageMode_OnlyConfiguredLANPrintersUseEpson(t *testing.T) {
	lanPrinter := printer.EncodeLANPrinterID("192.168.1.55")
	policy := AccessPolicy{
		LANImageMode: func(ip string) string {
			if ip == "192.168.1.55" {
				return string(escpos.ImageModeEpsonGraphics)
			}
			return string(escpos.ImageModeLegacy)
		},
	}
	testutil.ExpectedEqual(t, resolveImageMode(lanPrinter, policy), escpos.ImageModeEpsonGraphics)
	testutil.ExpectedEqual(t, resolveImageMode(lanPrinter, AccessPolicy{}), escpos.ImageModeLegacy)
	testutil.ExpectedEqual(t, resolveImageMode("", policy), escpos.ImageModeLegacy)
	testutil.ExpectedEqual(t, resolveImageMode("USB-not-LAN", policy), escpos.ImageModeLegacy)
}

func TestPrintData_CUPSModeDrawerStillUsesDirectESCPOS(t *testing.T) {
	_, _, err := testutil.StartMockTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		data := make([]byte, 256)
		_, _ = conn.Read(data)
	})
	testutil.ExpectedNoError(t, err)

	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr, AccessPolicy{
		LANImageMode: func(string) string { return "cups_driver" },
		LANCUPSQueue: func(string) string { return "TMTIENDA" },
	})
	defer s.Stop()

	id := printer.EncodeLANPrinterID("127.0.0.1")
	url := fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", id)
	req := httptest.NewRequest(http.MethodPost, url,
		bytes.NewBufferString(`<epos-print><pulse /></epos-print>`))
	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	data, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(data), `success="true"`)
}

func TestPrintData_CUPSModeWithoutQueueRefusesImage(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr, AccessPolicy{
		LANImageMode: func(string) string { return "cups_driver" },
	})
	defer s.Stop()

	id := printer.EncodeLANPrinterID("127.0.0.1")
	url := fmt.Sprintf("/p/%s/cgi-bin/epos/service.cgi", id)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewBufferString(
		`<epos-print><image width="8" height="1">AA==</image><cut /></epos-print>`))
	resp, err := testResponse(s, req)
	testutil.ExpectedNoError(t, err)
	data, err := io.ReadAll(resp.Body)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedContains(t, string(data), `success="false"`)
	testutil.ExpectedContains(t, string(data), `code="EX_BADPORT"`)
}

// Q3: USB, auto-selection (empty printer id) and a zero-value policy never
// inherit an experimental LAN image mode; only a configured LAN printer with
// its own stored mode can leave legacy.
func TestResolveImageMode_USBAndAutoSelectionStayLegacy(t *testing.T) {
	experimental := AccessPolicy{LANImageMode: func(string) string { return "cups_driver" }}
	testutil.ExpectedEqual(t, resolveImageMode("", experimental), escpos.ImageModeLegacy)
	testutil.ExpectedEqual(t, resolveImageMode("USB-not-LAN", experimental), escpos.ImageModeLegacy)
	testutil.ExpectedEqual(t, resolveImageMode("", AccessPolicy{}), escpos.ImageModeLegacy)
	testutil.ExpectedEqual(t, resolveImageMode("USB-not-LAN", AccessPolicy{}), escpos.ImageModeLegacy)
}

// CUPS receipts must report transport=cups in the trace; mixed jobs that fall
// back to direct ESC/POS keep their LAN/USB transport.
func TestTraceTransport_CUPSModeIsReported(t *testing.T) {
	lanID := printer.EncodeLANPrinterID("192.168.1.55")
	testutil.ExpectedEqual(t, traceTransport(lanID, escpos.ImageMode("cups_driver")), trace.TransportCUPS)
	testutil.ExpectedEqual(t, traceTransport(lanID, escpos.ImageModeLegacy), trace.TransportLAN)
	testutil.ExpectedEqual(t, traceTransport(lanID, escpos.ImageModeEpsonGraphics), trace.TransportLAN)
	testutil.ExpectedEqual(t, traceTransport("USB-not-LAN", escpos.ImageMode("cups_driver")), trace.TransportCUPS)
	testutil.ExpectedEqual(t, traceTransport("USB-not-LAN", escpos.ImageModeLegacy), trace.TransportUSB)
}
