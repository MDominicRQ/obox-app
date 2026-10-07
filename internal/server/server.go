package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"epos-proxy/internal/escpos"
	"epos-proxy/internal/logger"
	"epos-proxy/internal/printer"
)

const (
	maxRequestBodyBytes = int64(4 * 1024 * 1024)
	serverReadTimeout    = 15 * time.Second
	serverWriteTimeout   = 30 * time.Second
	serverIdleTimeout    = 60 * time.Second
	serverHeaderTimeout  = 5 * time.Second
	serverShutdownTimeout = 5 * time.Second
)

type EPOSResponse struct {
	XMLName xml.Name `xml:"response"`
	Success bool     `xml:"success,attr"`
	Code    string   `xml:"code,attr"`
	Status  string   `xml:"status,attr"`
}

// AccessPolicy controls whether non-loopback clients may use the proxy.
// A zero-value policy is permissive so package-level tests and embedders keep
// their historical behavior. Production passes the live application setting.
type AccessPolicy struct {
	NetworkPrintingEnabled func() bool
}

func (p AccessPolicy) remoteAllowed() bool {
	return p.NetworkPrintingEnabled == nil || p.NetworkPrintingEnabled()
}

type Server struct {
	app          http.Handler
	httpServer   *http.Server
	httpsServer  *http.Server
	Port         int
	HTTPSPort    int
	httpRunning  atomic.Bool
	httpsRunning atomic.Bool
	startErr     error
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func requestIP(r *http.Request) net.IP {
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	return net.ParseIP(host)
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Errorf("Recovered proxy panic method=%s path=%s remote=%s panic=%v", r.Method, r.URL.Path, r.RemoteAddr, recovered)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		logger.Infof(
			"Proxy request method=%s path=%s remote=%s origin=%q acrm=%q acrh=%q acrpn=%q status=%d acao=%q acam=%q acah=%q acapn=%q",
			r.Method,
			r.URL.Path,
			r.RemoteAddr,
			r.Header.Get("Origin"),
			r.Header.Get("Access-Control-Request-Method"),
			r.Header.Get("Access-Control-Request-Headers"),
			r.Header.Get("Access-Control-Request-Private-Network"),
			sw.status,
			sw.Header().Get("Access-Control-Allow-Origin"),
			sw.Header().Get("Access-Control-Allow-Methods"),
			sw.Header().Get("Access-Control-Allow-Headers"),
			sw.Header().Get("Access-Control-Allow-Private-Network"),
		)
	})
}

func withAccessPolicy(next http.Handler, policy AccessPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := requestIP(r)
		if ip != nil && ip.IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		if !policy.remoteAllowed() {
			http.Error(w, "Network printing is disabled", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			requestHeaders := r.Header.Get("Access-Control-Request-Headers")
			if requestHeaders == "" {
				requestHeaders = "Content-Type"
			}
			w.Header().Set("Access-Control-Allow-Headers", requestHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logger.Errorf("JSON response error: %v", err)
	}
}

func writeEPOSResponse(w http.ResponseWriter, response EPOSResponse) {
	data, err := xml.Marshal(response)
	if err != nil {
		logger.Errorf("XML response marshal error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return nil, err
		}
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return nil, err
	}
	return body, nil
}

func newHTTPHandler(mgr *printer.Manager, policy AccessPolicy) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": "ePOS proxy",
			"message": "ePOS Proxy is reachable",
		})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": "ePOS proxy",
		})
	})

	printerHealth := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"service":   "ePOS proxy",
			"printerId": r.PathValue("printerId"),
			"message":   "ePOS Proxy printer endpoint is reachable. Use this same address in Odoo without adding http:// or https://.",
		})
	}
	mux.HandleFunc("GET /p/{printerId}", printerHealth)
	mux.HandleFunc("GET /p/{printerId}/{$}", printerHealth)

	mux.HandleFunc("GET /p/{printerId}/cgi-bin/epos/service.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /cgi-bin/epos/service.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /p/{printerId}/cgi-bin/epos/service.cgi", func(w http.ResponseWriter, r *http.Request) {
		printerID := r.PathValue("printerId")
		logger.Infof("Print request received for printer: %s", printerID)
		printData(mgr, w, r, printerID)
	})
	mux.HandleFunc("POST /cgi-bin/epos/service.cgi", func(w http.ResponseWriter, r *http.Request) {
		logger.Infof("Print request received (auto printer selection)")
		printData(mgr, w, r, "")
	})
	mux.HandleFunc("POST /p/{printerId}/pstprnt", func(w http.ResponseWriter, r *http.Request) {
		printerID := r.PathValue("printerId")
		logger.Infof("Label print request received for printer: %s", printerID)
		printLabel(mgr, w, r, printerID)
	})

	return withRecovery(withRequestLogging(withAccessPolicy(withCORS(mux), policy)))
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: serverHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    16 * 1024,
	}
}

func New(port int, mgr *printer.Manager, policies ...AccessPolicy) *Server {
	return newServer(port, 0, "", "", mgr, policies...)
}

func NewWithTLS(port, httpsPort int, certFile, keyFile string, mgr *printer.Manager, policies ...AccessPolicy) *Server {
	return newServer(port, httpsPort, certFile, keyFile, mgr, policies...)
}

func newServer(port, httpsPort int, certFile, keyFile string, mgr *printer.Manager, policies ...AccessPolicy) *Server {
	policy := AccessPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}

	handler := newHTTPHandler(mgr, policy)
	server := &Server{
		app:       handler,
		Port:      port,
		HTTPSPort: httpsPort,
	}

	if port <= 0 {
		server.startErr = fmt.Errorf("invalid HTTP port %d", port)
		return server
	}

	httpListener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		server.startErr = fmt.Errorf("listen HTTP on port %d: %w", port, err)
		return server
	}

	server.httpServer = newHTTPServer(handler)
	server.httpRunning.Store(true)
	go func() {
		logger.Infof("HTTP server listening on 0.0.0.0:%d", port)
		err := server.httpServer.Serve(httpListener)
		server.httpRunning.Store(false)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("EPOS HTTP Server Error: %v", err)
		}
		logger.Warn("HTTP server stopped")
	}()

	if httpsPort > 0 && certFile != "" && keyFile != "" {
		certificate, certErr := tls.LoadX509KeyPair(certFile, keyFile)
		if certErr != nil {
			server.startErr = fmt.Errorf("load HTTPS certificate: %w", certErr)
			return server
		}

		httpsListener, listenErr := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", httpsPort))
		if listenErr != nil {
			server.startErr = fmt.Errorf("listen HTTPS on port %d: %w", httpsPort, listenErr)
			return server
		}

		server.httpsServer = newHTTPServer(handler)
		server.httpsServer.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
		server.httpsRunning.Store(true)
		go func() {
			logger.Infof("HTTPS server listening on 0.0.0.0:%d", httpsPort)
			tlsListener := tls.NewListener(httpsListener, server.httpsServer.TLSConfig)
			err := server.httpsServer.Serve(tlsListener)
			server.httpsRunning.Store(false)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Errorf("EPOS HTTPS Server Error: %v", err)
			}
			logger.Warn("HTTPS server stopped")
		}()
	}

	return server
}

func printData(mgr *printer.Manager, w http.ResponseWriter, r *http.Request, printerID string) {
	body, err := readBody(w, r)
	if err != nil {
		return
	}

	logger.Debugf("Processing print job for printer: %s", printerID)
	jobData, err := escpos.ParseXML(body)
	if err != nil {
		logger.Errorf("XML parsing error: %v", err)
		writeEPOSResponse(w, EPOSResponse{Success: false, Code: "SchemaError", Status: ""})
		return
	}

	reply, err := mgr.WriteAsync(printerID, jobData)
	if err == nil {
		result := <-reply
		if !result.OK {
			err = result.Err
		}
	}
	if err != nil {
		retCode := "EX_BADPORT"
		if errors.Is(err, printer.ErrQueueFull) {
			retCode = "TooManyRequests"
			logger.Warn("Printer queue full")
		}
		logger.Errorf("Print error [%s]: %v, Printer ID: %s", retCode, err, printerID)
		writeEPOSResponse(w, EPOSResponse{Success: false, Code: retCode, Status: ""})
		return
	}

	logger.Infof("Print job completed successfully for printer: %s", printerID)
	writeEPOSResponse(w, EPOSResponse{Success: true, Code: "", Status: ""})
}

func printLabel(mgr *printer.Manager, w http.ResponseWriter, r *http.Request, printerID string) {
	jobData, err := readBody(w, r)
	if err != nil {
		return
	}
	if len(jobData) == 0 {
		logger.Warn("Empty label data received")
		http.Error(w, "Empty label data", http.StatusBadRequest)
		return
	}

	reply, err := mgr.WriteAsync(printerID, jobData)
	if err == nil {
		result := <-reply
		if !result.OK {
			err = result.Err
		}
	}
	if err != nil {
		if errors.Is(err, printer.ErrQueueFull) {
			logger.Warnf("Printer queue full, Printer ID: %s", printerID)
			http.Error(w, "Printer queue full", http.StatusTooManyRequests)
			return
		}
		logger.Errorf("Print error: %v, Printer ID: %s", err, printerID)
		http.Error(w, "Printer error", http.StatusInternalServerError)
		return
	}

	logger.Infof("Print job completed successfully for printer: %s", printerID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) StartError() error {
	return s.startErr
}

func (s *Server) Stop() error {
	logger.Infof("Stopping proxy servers")

	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()

	var firstErr error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
			firstErr = err
		}
	}
	if s.httpsServer != nil {
		if err := s.httpsServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
			firstErr = err
		}
	}

	s.httpRunning.Store(false)
	s.httpsRunning.Store(false)
	return firstErr
}

func (s *Server) Running() bool {
	return s != nil && (s.httpRunning.Load() || s.httpsRunning.Load())
}

func (s *Server) HTTPSRunning() bool {
	return s != nil && s.httpsServer != nil && s.httpsRunning.Load()
}
