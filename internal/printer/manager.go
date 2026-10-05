package printer

import (
	"errors"
	"epos-proxy/internal/logger"
	"fmt"
	"sync"
)

const MaxCachedPrinters = 128

var (
	ErrLANPrinterNotAllowed = errors.New("LAN printer is not configured")
	ErrPrinterCacheFull      = errors.New("printer cache limit reached")
)

type Manager struct {
	mu             sync.RWMutex
	printers       map[string]*Printer
	allowLANPrinter func(string) bool
}

func NewManager(authorizers ...func(string) bool) *Manager {
	var authorizer func(string) bool
	if len(authorizers) > 0 {
		authorizer = authorizers[0]
	}
	return &Manager{
		printers:        make(map[string]*Printer),
		allowLANPrinter: authorizer,
	}
}

func (m *Manager) validateID(id string) error {
	if id == "" {
		// Empty ID is intentionally reserved for the legacy auto-select route.
		return nil
	}

	if lanIP, ok := DecodeLANPrinterID(id); ok {
		validatedIP, err := ValidateIPAddress(lanIP)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPrinterID, err)
		}
		if m.allowLANPrinter != nil && !m.allowLANPrinter(validatedIP) {
			return fmt.Errorf("%w: %s", ErrLANPrinterNotAllowed, validatedIP)
		}
		return nil
	}

	if _, err := decodePrinterID(id); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Get(id string) (*Printer, error) {
	if err := m.validateID(id); err != nil {
		return nil, err
	}

	m.mu.RLock()
	if p, ok := m.printers[id]; ok {
		m.mu.RUnlock()
		logger.Debugf("Reusing existing printer instance for ID: %s", id)
		return p, nil
	}
	m.mu.RUnlock()

	logger.Debugf("Creating new printer instance for ID: %s", id)
	p, err := newPrinter(id)
	if err != nil {
		return nil, err
	}
	if err := p.ensureOpen(); err != nil {
		p.close()
		return nil, fmt.Errorf("failed to open new printer instance for ID %s: %w", id, err)
	}

	m.mu.Lock()
	if existing, ok := m.printers[id]; ok {
		m.mu.Unlock()
		p.close()
		return existing, nil
	}
	if len(m.printers) >= MaxCachedPrinters {
		m.mu.Unlock()
		p.close()
		return nil, ErrPrinterCacheFull
	}
	m.printers[id] = p
	m.mu.Unlock()

	go p.loop()
	logger.Debugf("Registered new printer instance for ID: %s", id)
	return p, nil
}

func (m *Manager) WriteAsync(printerID string, data []byte) (<-chan JobResult, error) {
	p, err := m.Get(printerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get printer for ID %s: %w", printerID, err)
	}

	reply := make(chan JobResult, 1)
	err = p.Enqueue(func(p *Printer) JobResult {
		logger.Debugf("Executing print job for printer %s", printerID)
		if err := p.Write(data); err != nil {
			return JobResult{Err: fmt.Errorf("print job failed for printer %s: %w", printerID, err)}
		}
		logger.Debugf("Print job completed for printer %s", printerID)
		return JobResult{OK: true}
	}, reply)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue print job for printer %s: %w", printerID, err)
	}

	return reply, nil
}
