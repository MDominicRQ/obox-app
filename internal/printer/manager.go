package printer

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"epos-proxy/internal/logger"
	"epos-proxy/internal/trace"
)

const MaxCachedPrinters = 128

var (
	ErrLANPrinterNotAllowed = errors.New("LAN printer is not configured")
	ErrPrinterCacheFull     = errors.New("printer cache limit reached")
)

type Manager struct {
	mu              sync.RWMutex
	printers        map[string]*Printer
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
	return m.WriteAsyncContext(context.Background(), printerID, data, nil)
}

// WriteAsyncTraced queues a print job and attaches its per-ticket trace. A nil
// trace keeps the historical behaviour: the bytes, the queue and the device
// operations are identical either way.
func (m *Manager) WriteAsyncTraced(printerID string, data []byte, tr *trace.Trace) (<-chan JobResult, error) {
	return m.WriteAsyncContext(context.Background(), printerID, data, tr)
}

// WriteAsyncContext queues a print job bound to the request context.
//
// The context is only consulted once, when the printer loop is about to write
// to the device: if the client already abandoned the request, the job is
// skipped and reports ErrCanceled, because printing it would add an orphan
// ticket the POS already reported as failed. Once the write starts it is never
// interrupted, so a job that reached the device always completes and reports
// its real result.
//
// No path in the manager re-sends a job on failure: a retry can only come from
// a new client request.
func (m *Manager) WriteAsyncContext(ctx context.Context, printerID string, data []byte, tr *trace.Trace) (<-chan JobResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if tr != nil {
		tr.MarkEnqueue()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if tr != nil {
			tr.MarkClientCanceled(trace.CancelStageBeforeEnqueue)
		}
		return nil, fmt.Errorf("%w: %v", ErrCanceled, ctxErr)
	}

	p, err := m.Get(printerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get printer for ID %s: %w", printerID, err)
	}

	reply := make(chan JobResult, 1)
	err = p.EnqueueTraced(func(p *Printer) JobResult {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if tr != nil {
				tr.MarkClientCanceled(trace.CancelStageQueued)
			}
			logger.Warnf("Skipping queued print job for printer %s: client abandoned the request: %v", printerID, ctxErr)
			return JobResult{Err: fmt.Errorf("%w: %v", ErrCanceled, ctxErr)}
		}
		if tr != nil {
			tr.MarkJobStart()
		}
		logger.Debugf("Executing print job for printer %s", printerID)
		if err := p.write(data, tr); err != nil {
			return JobResult{Err: fmt.Errorf("print job failed for printer %s: %w", printerID, err)}
		}
		logger.Debugf("Print job completed for printer %s", printerID)
		return JobResult{OK: true}
	}, reply, tr)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue print job for printer %s: %w", printerID, err)
	}
	if tr != nil {
		tr.MarkQueued()
	}

	return reply, nil
}
