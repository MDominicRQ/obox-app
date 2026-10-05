package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"epos-proxy/internal/certs"
	"epos-proxy/internal/config"
	"epos-proxy/internal/logger"
	"epos-proxy/internal/printer"
	"epos-proxy/internal/server"
	"epos-proxy/internal/util"

	autostart "github.com/emersion/go-autostart"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dialoger abstracts the Wails runtime dialog calls. Production code uses
// runtimeDialogs; tests substitute a fake so the dialog-driven code paths can
// be exercised without a live Wails context.
type dialoger interface {
	Message(ctx context.Context, opts wailsruntime.MessageDialogOptions) (string, error)
	SaveFile(ctx context.Context, opts wailsruntime.SaveDialogOptions) (string, error)
}

// runtimeDialogs forwards to the real Wails runtime.
type runtimeDialogs struct{}

func (runtimeDialogs) Message(ctx context.Context, opts wailsruntime.MessageDialogOptions) (string, error) {
	return wailsruntime.MessageDialog(ctx, opts)
}

func (runtimeDialogs) SaveFile(ctx context.Context, opts wailsruntime.SaveDialogOptions) (string, error) {
	return wailsruntime.SaveFileDialog(ctx, opts)
}

// App struct
type App struct {
	ctx             context.Context
	webserver       *server.Server
	config          *config.Manager
	printerManager  *printer.Manager
	autoStart       *autostart.App
	dialogs         dialoger
	httpsCACertPath string
}

// dlg returns the dialog backend, defaulting to the Wails runtime so an App
// built as a bare struct literal still behaves correctly.
func (a *App) dlg() dialoger {
	if a.dialogs == nil {
		return runtimeDialogs{}
	}
	return a.dialogs
}

// showError surfaces an error to the user and logs any failure to do so.
func (a *App) showError(title, message string) {
	if _, err := a.dlg().Message(a.ctx, wailsruntime.MessageDialogOptions{
		Type:    wailsruntime.ErrorDialog,
		Title:   title,
		Message: message,
	}); err != nil {
		logger.Errorf("Failed to show error dialog %q: %v", title, err)
	}
}

type Printer struct {
	Name           string `json:"name"`
	Ip             string `json:"ip"`
	HTTPSIp        string `json:"httpsIp,omitempty"`
	NetworkIp      string `json:"networkIp,omitempty"`
	NetworkHTTPSIp string `json:"networkHttpsIp,omitempty"`
	Id             string `json:"id"`
	IsLAN          bool   `json:"isLAN"`
	LANIp          string `json:"lanIp,omitempty"`
	Online         bool   `json:"online"`
	Type           string `json:"type"`
}

type UnavailablePrinter struct {
	Name     string `json:"name"`
	ErrorMsg string `json:"errorMsg"`
	IsLAN    bool   `json:"isLAN"`
	LANIp    string `json:"lanIp,omitempty"`
}

type AppVariable struct {
	ServerRunning bool   `json:"serverRunning"`
	Os            string `json:"os"`
}

type Printers struct {
	ErrorMsg            string               `json:"errorMsg"`
	Printers            []Printer            `json:"printers"`
	UnavailablePrinters []UnavailablePrinter `json:"unavailablePrinters"`
}

func NewApp() *App {
	a := &App{}

	a.autoStart = &autostart.App{
		Name:        "epos-proxy",
		DisplayName: "ePOS Proxy",
		Exec:        []string{os.Args[0], "--background"},
	}
	a.printerManager = printer.NewManager()
	a.dialogs = runtimeDialogs{}

	cfg, err := config.NewManager()
	if err != nil {
		logger.Fatalf("Config initialization failed: %v", err)
	}

	if err := cfg.Load(); err != nil {
		logger.Warnf("Config load warning: %v", err)
	}

	a.config = cfg

	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	logger.Debugf("Application startup")
	logger.Debugf("Config loaded from %s", a.config.Path())

	a.triggerLocalNetworkPermissionProbe()

	port, err := a.config.ResolvePort()
	if err != nil {
		logger.Errorf("Unable to resolve HTTP port: %v", err)
		a.webserver = server.New(port, a.printerManager)
		return
	}

	httpsPort, err := a.config.ResolveHTTPSPort(port)
	if err != nil {
		logger.Warnf("HTTPS disabled because no HTTPS port could be resolved: %v", err)
		a.webserver = server.New(port, a.printerManager)
		return
	}

	lanIPs := util.GetLocalIPv4Addresses()
	logger.Infof("Local IPv4 addresses available for HTTPS SANs: %v", lanIPs)

	certPaths, err := certs.Ensure(filepath.Dir(a.config.Path()), lanIPs...)
	if err != nil {
		logger.Warnf("HTTPS disabled because local certificates could not be prepared: %v", err)
		a.webserver = server.New(port, a.printerManager)
		return
	}

	a.httpsCACertPath = certPaths.CACert
	a.webserver = server.NewWithTLS(
		port,
		httpsPort,
		certPaths.ServerCert,
		certPaths.ServerKey,
		a.printerManager,
	)
}

func (a *App) shutdown(ctx context.Context) {
	logger.Infof("Stopping proxy server")

	if a.webserver == nil {
		return
	}
	if err := a.webserver.Stop(); err != nil {
		logger.Errorf("Server stop error: %v", err)
	}
}

func (a *App) AppVariable() AppVariable {
	return AppVariable{
		Os:            runtime.GOOS,
		ServerRunning: a.webserver.Running(),
	}
}

func (a *App) GetPrinterUrl(id string) string {
	if a.webserver == nil {
		return ""
	}
	url := fmt.Sprintf("%s:%d/p/%s", util.LOCALHOST_IP, a.webserver.Port, id)
	logger.Debugf("Generated local HTTP printer endpoint: %s", url)
	return url
}

func (a *App) GetPrinterHTTPSUrl(id string) string {
	if a.webserver == nil || a.webserver.HTTPSPort <= 0 {
		return ""
	}
	url := fmt.Sprintf("%s:%d/p/%s", util.LOCALHOST_IP, a.webserver.HTTPSPort, id)
	logger.Debugf("Generated local HTTPS printer endpoint: %s", url)
	return url
}

func (a *App) GetPrinterNetworkUrl(id string) string {
	if a.webserver == nil || !a.config.IsNetworkPrintingEnabled() {
		return ""
	}
	host := util.GetLocalIP(true)
	if host == util.LOCALHOST_IP {
		return ""
	}
	url := fmt.Sprintf("%s:%d/p/%s", host, a.webserver.Port, id)
	logger.Debugf("Generated LAN HTTP printer endpoint: %s", url)
	return url
}

func (a *App) GetPrinterNetworkHTTPSUrl(id string) string {
	if a.webserver == nil || a.webserver.HTTPSPort <= 0 || !a.config.IsNetworkPrintingEnabled() {
		return ""
	}
	host := util.GetLocalIP(true)
	if host == util.LOCALHOST_IP {
		return ""
	}
	url := fmt.Sprintf("%s:%d/p/%s", host, a.webserver.HTTPSPort, id)
	logger.Debugf("Generated LAN HTTPS printer endpoint: %s", url)
	return url
}

func (a *App) Printers() Printers {

	logger.Debug("Collecting printer status")

	printers := make([]Printer, 0)
	unavailablePrinters := make([]UnavailablePrinter, 0)

	printerInfos, err := printer.ListUSBPrinters()
	errorMsg := ""
	if err == nil {

		logger.Debugf("Detected %d available USB printers", len(printerInfos.Available))

		for _, info := range printerInfos.Available {
			printers = append(printers, Printer{
				Id:             info.Id,
				Name:           info.Name,
				Ip:             a.GetPrinterUrl(info.Id),
				HTTPSIp:        a.GetPrinterHTTPSUrl(info.Id),
				NetworkIp:      a.GetPrinterNetworkUrl(info.Id),
				NetworkHTTPSIp: a.GetPrinterNetworkHTTPSUrl(info.Id),
				Online:         true,
				Type:           string(info.Type),
			})
		}

		for _, info := range printerInfos.Unavailable {
			unavailablePrinters = append(unavailablePrinters, UnavailablePrinter{
				Name:     info.Name,
				ErrorMsg: info.Error,
			})

			logger.Warnf("USB printer unavailable: %s (%s)", info.Name, info.Error)
		}
	} else {
		errorMsg = err.Error()
		logger.Errorf("USB printer detection failed: %v", err)
	}

	lanPrinters := printer.ListLANPrinters(a.config)

	for _, info := range lanPrinters {
		printers = append(printers, Printer{
			Id:             info.Id,
			Name:           fmt.Sprintf("Network - %s", info.IP),
			Ip:             a.GetPrinterUrl(info.Id),
			HTTPSIp:        a.GetPrinterHTTPSUrl(info.Id),
			NetworkIp:      a.GetPrinterNetworkUrl(info.Id),
			NetworkHTTPSIp: a.GetPrinterNetworkHTTPSUrl(info.Id),
			IsLAN:          true,
			LANIp:          info.IP,
			Type:           string(printer.TypeReceipt),
		})
	}

	return Printers{
		Printers:            printers,
		UnavailablePrinters: unavailablePrinters,
		ErrorMsg:            errorMsg,
	}
}

func (a *App) AddLANPrinter(ip string) error {
	logger.Debugf("Adding LAN printer: %s", ip)

	ip, err := printer.ValidateIPAddress(ip)
	if err != nil {
		return fmt.Errorf("invalid IP address: %s, error: %v", ip, err)
	}

	if err := printer.CheckLANPrinter(ip); err != nil {
		return fmt.Errorf("LAN printer unreachable: %s, error: %v", ip, err)
	}

	if err := a.config.AddLanEposPrinter(ip); err != nil {
		return fmt.Errorf("failed to save LAN printer: %s, error: %v", ip, err)
	}

	logger.Debugf("LAN printer added successfully: %s", ip)
	return nil
}

func (a *App) ConfirmRemoveLANPrinter(ip string) (bool, error) {
	logger.Debugf("Remove LAN printer requested: %s", ip)

	result, err := a.dlg().Message(a.ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.QuestionDialog,
		Title:         "Remove Printer",
		Message:       fmt.Sprintf("Are you sure you want to remove the printer at %s?", ip),
		Buttons:       []string{"Cancel", "Confirm"},
		DefaultButton: "Cancel",
		CancelButton:  "Cancel",
	})
	if err != nil {
		return false, fmt.Errorf("failed to show confirmation dialog: %w", err)
	}
	if result == "Confirm" || result == "Yes" {
		if err := a.config.RemoveLANPrinter(ip); err != nil {
			return false, fmt.Errorf("failed to remove LAN printer: %w", err)
		}
		return true, nil
	}
	logger.Infof("Remove LAN printer cancelled, Remove printer dialog result: %s", result)
	return false, nil
}

func (a *App) CheckLANPrinterStatus(ip string) bool {
	logger.Debugf("Checking LAN printer status: %s", ip)
	return printer.CheckLANPrinter(ip) == nil
}

func (a *App) DownloadLogs() {
	logger.Debugf("Download logs requested")
	logDir := logger.LogDirectory()
	zipName := fmt.Sprintf("epos-proxy-logs-%s.zip",
		time.Now().Format("2006-01-02"))
	logger.Debugf("Creating logs archive: %s", zipName)
	savePath, err := a.dlg().SaveFile(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "Save Archive",
		DefaultFilename: zipName,
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "Zip Archives (*.zip)",
				Pattern:     "*.zip",
			},
		},
	})
	if err != nil {
		logger.Errorf("Save dialog failed: %v", err)
		a.showError("Download Logs Failed", err.Error())
		return
	}

	// An empty path means the user dismissed the save dialog.
	if savePath == "" {
		logger.Infof("Download logs cancelled by user")
		return
	}

	if err := util.ZipLogs(logDir, savePath); err != nil {
		logger.Errorf("Log export failed: %v", err)
		a.showError("Download Logs Failed", err.Error())
		return
	}
	logger.Infof("Logs successfully exported to: %s", savePath)
}

func (a *App) triggerLocalNetworkPermissionProbe() {
	if runtime.GOOS != "darwin" || a.config == nil {
		return
	}

	ips := a.config.GetLANPrinters()
	if len(ips) == 0 {
		return
	}

	go func() {
		time.Sleep(750 * time.Millisecond)
		for _, ip := range ips {
			logger.Infof("Probing configured LAN printer %s to request/verify macOS Local Network access", ip)
			if err := printer.CheckLANPrinter(ip); err != nil {
				logger.Warnf("LAN permission/connectivity probe failed for %s:%d: %v", ip, printer.LANPort, err)
				continue
			}
			logger.Infof("LAN permission/connectivity probe succeeded for %s:%d", ip, printer.LANPort)
		}
	}()
}

func macOSFirewallStatus() string {
	if runtime.GOOS != "darwin" {
		return "not applicable"
	}

	out, err := exec.Command(
		"/usr/libexec/ApplicationFirewall/socketfilterfw",
		"--getglobalstate",
	).CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			return "unknown: " + string(out)
		}
		return "unknown: " + err.Error()
	}
	return string(out)
}

func probeProxyURL(rawURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	return nil
}

func (a *App) ShowProxyDiagnostics() {
	if a.webserver == nil {
		a.showError("Proxy Diagnostics", "The proxy server has not started.")
		return
	}

	httpURL := fmt.Sprintf("http://%s:%d/healthz", util.LOCALHOST_IP, a.webserver.Port)
	httpsURL := ""
	if a.webserver.HTTPSPort > 0 {
		httpsURL = fmt.Sprintf("https://%s:%d/healthz", util.LOCALHOST_IP, a.webserver.HTTPSPort)
	}

	httpStatus := "OK"
	if err := probeProxyURL(httpURL); err != nil {
		httpStatus = "FAILED: " + err.Error()
	}

	httpsStatus := "disabled"
	if httpsURL != "" {
		httpsStatus = "OK"
		if err := probeProxyURL(httpsURL); err != nil {
			httpsStatus = "FAILED: " + err.Error()
		}
	}

	lanIPs := util.GetLocalIPv4Addresses()
	lanHost := util.GetLocalIP(true)
	lanHTTPURL := ""
	lanHTTPStatus := "unavailable"
	if lanHost != "" && lanHost != util.LOCALHOST_IP {
		lanHTTPURL = fmt.Sprintf("http://%s:%d/healthz", lanHost, a.webserver.Port)
		lanHTTPStatus = "OK"
		if err := probeProxyURL(lanHTTPURL); err != nil {
			lanHTTPStatus = "FAILED: " + err.Error()
		}
	}

	printerLines := ""
	for _, ip := range a.config.GetLANPrinters() {
		status := "OK"
		if err := printer.CheckLANPrinter(ip); err != nil {
			status = "FAILED: " + err.Error()
		}
		printerLines += fmt.Sprintf("\nPrinter %s:%d: %s", ip, printer.LANPort, status)
	}
	if printerLines == "" {
		printerLines = "\nNo LAN printers are configured."
	}

	message := fmt.Sprintf(
		"Local HTTP: %s\n%s\n\nLocal HTTPS: %s\n%s\n\nDetected LAN IPv4 addresses: %v\nSelected LAN address: %s\n\nLAN HTTP self-check: %s\n%s\n\nLAN printer connectivity:%s\n\nmacOS Application Firewall: %s\n\nFor a remote Odoo/POS, test the selected LAN HTTP URL from the device that actually runs Odoo. If Odoo still reports unreachable, reproduce the failure and export the logs immediately afterwards. The logs now record incoming requests and CORS/LNA-related response headers at Info level.",
		httpStatus,
		httpURL,
		httpsStatus,
		httpsURL,
		lanIPs,
		lanHost,
		lanHTTPStatus,
		lanHTTPURL,
		printerLines,
		macOSFirewallStatus(),
	)

	if _, err := a.dlg().Message(a.ctx, wailsruntime.MessageDialogOptions{
		Type:    wailsruntime.InfoDialog,
		Title:   "Proxy Diagnostics",
		Message: message,
	}); err != nil {
		logger.Errorf("Failed to show proxy diagnostics: %v", err)
	}
}

func (a *App) InstallHTTPSCertificate() error {
	if a.httpsCACertPath == "" {
		return fmt.Errorf("HTTPS certificate is not available; check the application logs for TLS startup errors")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", a.httpsCACertPath)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", a.httpsCACertPath)
	default:
		cmd = exec.Command("xdg-open", a.httpsCACertPath)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open HTTPS CA certificate: %w", err)
	}

	message := "The ePOS Proxy local CA certificate has been opened. Mark it as trusted in your operating system before using the HTTPS address from Odoo."
	if runtime.GOOS == "darwin" {
		message = "The ePOS Proxy local CA certificate has been opened in Keychain Access. Import it, open 'ePOS Proxy Local CA', expand Trust, and set 'When using this certificate' to 'Always Trust'. macOS may request an administrator password. ePOS Proxy does not run privileged trust commands silently."
	}

	_, err := a.dlg().Message(a.ctx, wailsruntime.MessageDialogOptions{
		Type:    wailsruntime.InfoDialog,
		Title:   "Trust HTTPS Certificate",
		Message: message,
	})
	if err != nil {
		return fmt.Errorf("show HTTPS certificate instructions: %w", err)
	}
	return nil
}

func (a *App) IsAutostartEnabled() bool {
	return a.autoStart.IsEnabled()
}

func (a *App) EnableAutostart() error {
	logger.Info("Enabling autostart")

	if runtime.GOOS == "linux" {
		return util.EnableLinuxAutostart()
	}

	if !a.autoStart.IsEnabled() {
		return a.autoStart.Enable()
	}

	return nil
}

func (a *App) DisableAutostart() error {
	logger.Info("Disabling autostart")

	if a.autoStart.IsEnabled() {
		return a.autoStart.Disable()
	}

	return nil
}

func (a *App) SetNetworkPrintingEnabled(enabled bool) error {
	logger.Infof("Setting network printing enabled: %v", enabled)
	return a.config.SetNetworkPrintingEnabled(enabled)
}

func (a *App) IsNetworkPrintingEnabled() bool {
	if a.config == nil {
		return false
	}
	return a.config.IsNetworkPrintingEnabled()
}

type TroubleshootInfo struct {
	ActiveFirewall string   `json:"activeFirewall"`
	FirewallZone   string   `json:"firewallZone"`
	Port           int      `json:"port"`
	HTTPSPort      int      `json:"httpsPort"`
	HTTPSCACert    string   `json:"httpsCaCert"`
	Subnet         string   `json:"subnet"`
	LocalIP        string   `json:"localIp"`
	LocalIPv4s     []string `json:"localIPv4s"`
	ExecPath       string   `json:"execPath"`
}

func (a *App) GetTroubleshootInfo() TroubleshootInfo {
	netInfo := util.GetNetworkInfo()
	execPath, _ := os.Executable()
	return TroubleshootInfo{
		ActiveFirewall: netInfo.ActiveFirewall,
		FirewallZone:   netInfo.Zone,
		Port:           a.config.GetPort(),
		HTTPSPort:      a.config.GetHTTPSPort(),
		HTTPSCACert:    a.httpsCACertPath,
		Subnet:         netInfo.Subnet,
		LocalIP:        netInfo.IP,
		LocalIPv4s:     util.GetLocalIPv4Addresses(),
		ExecPath:       execPath,
	}
}
