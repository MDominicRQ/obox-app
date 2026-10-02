package server

import (
	"encoding/xml"
	"errors"
	"fmt"
	"sync/atomic"

	"epos-proxy/internal/escpos"
	"epos-proxy/internal/logger"
	"epos-proxy/internal/printer"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

type EPOSResponse struct {
	XMLName xml.Name `xml:"response"`
	Success bool     `xml:"success,attr"`
	Code    string   `xml:"code,attr"`
	Status  string   `xml:"status,attr"`
}

type Server struct {
	app          *fiber.App
	httpsApp     *fiber.App
	Port         int
	HTTPSPort    int
	httpRunning  atomic.Bool
	httpsRunning atomic.Bool
}

func newFiberApp(mgr *printer.Manager) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName: "ePOS proxy",
	})
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			return origin != ""
		},
		AllowPrivateNetwork: true,
	}))

	app.Get("/", func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"status":  "ok",
			"service": app.Config().AppName,
			"message": "ePOS Proxy is reachable",
		})
	})

	app.Get("/healthz", func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"status":  "ok",
			"service": app.Config().AppName,
		})
	})

	printerHealth := func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"status":    "ok",
			"service":   app.Config().AppName,
			"printerId": ctx.Params("printerId"),
			"message":   "ePOS Proxy printer endpoint is reachable. Use this same address in Odoo without adding http:// or https://.",
		})
	}
	app.Get("/p/:printerId", printerHealth)
	app.Get("/p/:printerId/", printerHealth)

	app.Get("/p/:printerId/cgi-bin/epos/service.cgi", func(ctx fiber.Ctx) error {
		return ctx.Status(fiber.StatusOK).Send([]byte{})
	})

	app.Get("/cgi-bin/epos/service.cgi", func(ctx fiber.Ctx) error {
		return ctx.Status(fiber.StatusOK).Send([]byte{})
	})

	app.Post("/p/:printerId/cgi-bin/epos/service.cgi", func(ctx fiber.Ctx) error {
		printerId := ctx.Params("printerId")
		logger.Debugf("Print request received for printer: %s", printerId)
		return printData(mgr, ctx, printerId)
	})

	app.Post("/cgi-bin/epos/service.cgi", func(ctx fiber.Ctx) error {
		logger.Debugf("Print request received (auto printer selection)")
		return printData(mgr, ctx, "")
	})

	app.Post("/p/:printerId/pstprnt", func(ctx fiber.Ctx) error {
		printerId := ctx.Params("printerId")
		logger.Debugf("Label print request received for printer: %s", printerId)
		return printLabel(mgr, ctx, printerId)
	})

	return app
}

func New(port int, mgr *printer.Manager) *Server {
	return newServer(port, 0, "", "", mgr)
}

func NewWithTLS(port, httpsPort int, certFile, keyFile string, mgr *printer.Manager) *Server {
	return newServer(port, httpsPort, certFile, keyFile, mgr)
}

func newServer(port, httpsPort int, certFile, keyFile string, mgr *printer.Manager) *Server {
	server := &Server{
		app:       newFiberApp(mgr),
		Port:      port,
		HTTPSPort: httpsPort,
	}

	server.httpRunning.Store(true)
	go func() {
		logger.Infof("HTTP server listening on 0.0.0.0:%d", port)
		err := server.app.Listen(fmt.Sprintf("0.0.0.0:%d", port))
		server.httpRunning.Store(false)
		if err != nil {
			logger.Error("EPOS HTTP Server Error: ", err)
		}
		logger.Warn("HTTP server stopped")
	}()

	if httpsPort > 0 && certFile != "" && keyFile != "" {
		server.httpsApp = newFiberApp(mgr)
		server.httpsRunning.Store(true)
		go func() {
			logger.Infof("HTTPS server listening on 0.0.0.0:%d", httpsPort)
			err := server.httpsApp.Listen(
				fmt.Sprintf("0.0.0.0:%d", httpsPort),
				fiber.ListenConfig{
					CertFile:    certFile,
					CertKeyFile: keyFile,
				},
			)
			server.httpsRunning.Store(false)
			if err != nil {
				logger.Error("EPOS HTTPS Server Error: ", err)
			}
			logger.Warn("HTTPS server stopped")
		}()
	}

	return server
}

func printData(mgr *printer.Manager, ctx fiber.Ctx, printerID string) error {
	logger.Debugf("Processing print job for printer: %s", printerID)
	jobData, err := escpos.ParseXML(ctx.Body())
	if err != nil {
		logger.Errorf("XML parsing error: %v", err)
		return ctx.XML(EPOSResponse{Success: false, Code: "SchemaError", Status: ""})
	}
	logger.Debug("XML parsed successfully")

	reply, err := mgr.WriteAsync(printerID, jobData)
	if err == nil {
		logger.Debug("Print job queued")
		result := <-reply
		if !result.OK {
			err = result.Err
		}
	}
	if err != nil {
		retCode := ""
		if errors.Is(err, printer.ErrQueueFull) {
			retCode = "TooManyRequests"
			logger.Warn("Printer queue full")
		} else {
			retCode = "EX_BADPORT"
		}
		logger.Errorf("Print error [%s]: %v, Printer ID: %s", retCode, err, printerID)
		return ctx.XML(EPOSResponse{Success: false, Code: retCode, Status: ""})
	}
	logger.Debugf("Print job completed successfully for printer: %s", printerID)
	return ctx.XML(EPOSResponse{Success: true, Code: "", Status: ""})
}

func printLabel(mgr *printer.Manager, ctx fiber.Ctx, printerID string) error {
	jobData := ctx.Body()

	if len(jobData) == 0 {
		logger.Warn("Empty label data received")
		return ctx.SendStatus(fiber.StatusBadRequest)
	}

	logger.Debugf("Processing label print job for printer: %s", printerID)

	reply, err := mgr.WriteAsync(printerID, jobData)
	if err == nil {
		logger.Debug("Label print job queued")
		result := <-reply
		if !result.OK {
			err = result.Err
		}
	}

	if err != nil {
		if errors.Is(err, printer.ErrQueueFull) {
			logger.Warnf("Printer queue full, Printer ID: %s", printerID)
			return ctx.SendStatus(fiber.StatusTooManyRequests)
		}

		logger.Errorf("Print error: %v, Printer ID: %s", err, printerID)
		return ctx.SendStatus(fiber.StatusInternalServerError)
	}

	logger.Debugf("Print job completed successfully for printer: %s", printerID)
	return ctx.SendStatus(fiber.StatusOK)
}

func (s *Server) Stop() error {
	logger.Infof("Stopping proxy servers")
	s.httpRunning.Store(false)
	s.httpsRunning.Store(false)

	var firstErr error
	if s.app != nil {
		if err := s.app.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.httpsApp != nil {
		if err := s.httpsApp.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) Running() bool {
	return s.httpRunning.Load() || s.httpsRunning.Load()
}

func (s *Server) HTTPSRunning() bool {
	return s.httpsApp != nil && s.httpsRunning.Load()
}
