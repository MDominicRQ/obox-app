package server

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"epos-proxy/internal/certs"
	"epos-proxy/internal/printer"
	"epos-proxy/internal/testutil"
)

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

	resp, err := s.app.Test(req)
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

	resp, err := s.app.Test(req)
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

	resp, err := s.app.Test(req)
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

	resp, err := s.app.Test(req)
	testutil.ExpectedNoError(t, err)
	testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)
}

func TestPrintLabel_EmptyBody_BadRequest(t *testing.T) {
	port := testutil.GetFreePort(t)
	mgr := printer.NewManager()
	s := New(port, mgr)
	defer s.Stop()

	req := httptest.NewRequest("POST", "/p/any-printer/pstprnt", bytes.NewReader([]byte{}))
	resp, err := s.app.Test(req)
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

	resp, err := s.app.Test(req)
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

	resp, err := s.app.Test(req)
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

			resp, err := s.app.Test(req)
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
		resp, err := s.app.Test(req)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedEqual(t, resp.StatusCode, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		testutil.ExpectedNoError(t, err)
		testutil.ExpectedContains(t, string(body), `"status":"ok"`)
	}

	req := httptest.NewRequest(http.MethodGet, "/p/test-printer", nil)
	resp, err := s.app.Test(req)
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
		resp, err := s.app.Test(req)
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
