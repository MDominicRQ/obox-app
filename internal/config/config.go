package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
)

var ErrNoAvailablePort = errors.New("no available port in range")

const AppName = "EposProxy"

const (
	PortRangeStart      = 4545
	PortRangeEnd        = 4555
	HTTPSPortRangeStart = 4645
	HTTPSPortRangeEnd   = 4655
)

type AppConfig struct {
	Port            int      `json:"port"`
	HTTPSPort       int      `json:"https_port,omitempty"`
	LANPrinters     []string `json:"lan_printers,omitempty"`
	NetworkPrinting bool     `json:"network_printing"`
}

func defaults() AppConfig {
	return AppConfig{
		Port:            0,
		HTTPSPort:       0,
		NetworkPrinting: false,
	}
}

type Manager struct {
	mu   sync.RWMutex
	path string
	Data AppConfig
}

func NewManager() (*Manager, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("cannot locate user config dir: %w", err)
	}

	dir := filepath.Join(base, AppName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create config dir: %w", err)
	}

	return &Manager{
		path: filepath.Join(dir, "config.json"),
		Data: defaults(),
	}, nil
}

func (cm *Manager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(cm.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config read error: %w", err)
	}

	loaded := defaults()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("config parse error: %w", err)
	}
	cm.Data = loaded
	return nil
}

func (cm *Manager) Save() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.saveLocked()
}

func (cm *Manager) saveLocked() error {
	data, err := json.MarshalIndent(cm.Data, "", "  ")
	if err != nil {
		return fmt.Errorf("config marshal error: %w", err)
	}

	dir := filepath.Dir(cm.path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config temp file error: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config temp permissions error: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config temp write error: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config temp sync error: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config temp close error: %w", err)
	}

	if err := os.Rename(tmpPath, cm.path); err != nil {
		// Windows does not always replace an existing destination atomically.
		// Fall back to remove+rename there rather than reverting to in-place writes.
		if removeErr := os.Remove(cm.path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("config replace error: %w", err)
		}
		if retryErr := os.Rename(tmpPath, cm.path); retryErr != nil {
			return fmt.Errorf("config replace error: %w", retryErr)
		}
	}
	return nil
}

func (cm *Manager) Path() string { return cm.path }

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func findAvailablePort(start, end int) (int, error) {
	return findAvailablePortExcluding(start, end, 0)
}

func findAvailablePortExcluding(start, end, excluded int) (int, error) {
	for p := start; p <= end; p++ {
		if p != excluded && isPortAvailable(p) {
			return p, nil
		}
	}

	return 0, fmt.Errorf("no available port found in range %d-%d: %w", start, end, ErrNoAvailablePort)
}

func (cm *Manager) ResolvePort() (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.Data.Port > 0 {
		if isPortAvailable(cm.Data.Port) {
			return cm.Data.Port, nil
		}
		return 0, fmt.Errorf("configured HTTP port %d is already in use", cm.Data.Port)
	}

	port, err := findAvailablePort(PortRangeStart, PortRangeEnd)
	if err != nil {
		return 0, err
	}

	cm.Data.Port = port
	if err := cm.saveLocked(); err != nil {
		log.Printf("[config] warning: could not save: %v\n", err)
	}
	return port, nil
}

func (cm *Manager) GetPort() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.Data.Port
}

func (cm *Manager) ResolveHTTPSPort(httpPort int) (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.Data.HTTPSPort > 0 {
		if cm.Data.HTTPSPort == httpPort {
			return 0, fmt.Errorf("configured HTTPS port %d conflicts with the HTTP port", cm.Data.HTTPSPort)
		}
		if isPortAvailable(cm.Data.HTTPSPort) {
			return cm.Data.HTTPSPort, nil
		}
		return 0, fmt.Errorf("configured HTTPS port %d is already in use", cm.Data.HTTPSPort)
	}

	port, err := findAvailablePortExcluding(HTTPSPortRangeStart, HTTPSPortRangeEnd, httpPort)
	if err != nil {
		return 0, err
	}

	cm.Data.HTTPSPort = port
	if err := cm.saveLocked(); err != nil {
		log.Printf("[config] warning: could not save HTTPS port: %v\n", err)
	}
	return port, nil
}

func (cm *Manager) GetHTTPSPort() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.Data.HTTPSPort
}

func (cm *Manager) SetNetworkPrintingEnabled(enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.Data.NetworkPrinting = enabled
	return cm.saveLocked()
}

func (cm *Manager) IsNetworkPrintingEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.Data.NetworkPrinting
}

func (cm *Manager) AddLanEposPrinter(ip string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, existing := range cm.Data.LANPrinters {
		if existing == ip {
			return nil // Already exists
		}
	}
	cm.Data.LANPrinters = append(cm.Data.LANPrinters, ip)
	return cm.saveLocked()
}

func (cm *Manager) RemoveLANPrinter(ip string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, existing := range cm.Data.LANPrinters {
		if existing == ip {
			cm.Data.LANPrinters = append(cm.Data.LANPrinters[:i], cm.Data.LANPrinters[i+1:]...)
			return cm.saveLocked()
		}
	}
	return nil // Not found, nothing to remove
}

func (cm *Manager) GetLANPrinters() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.Data.LANPrinters == nil {
		return []string{}
	}
	// Return a copy to avoid races if caller modifies the slice
	result := make([]string, len(cm.Data.LANPrinters))
	copy(result, cm.Data.LANPrinters)
	return result
}

func (cm *Manager) HasLANPrinter(ip string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, configured := range cm.Data.LANPrinters {
		if configured == ip {
			return true
		}
	}
	return false
}
