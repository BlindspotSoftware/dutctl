// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"go.bug.st/serial"
)

const reconnectInterval = 250 * time.Millisecond

// errDisconnected marks a boundary across which filters and expect buffers cannot carry state.
var errDisconnected = errors.New("serial device disconnected")

type portConfigurationError struct{ error }

func (e *portConfigurationError) Unwrap() error { return e.error }

// reconnectingPort has one reader, which owns opening, closing and notifications.
// The input writer shares mu so a disconnected handle is never closed during Write.
type reconnectingPort struct {
	ctx         context.Context //nolint:containedctx // io.Reader/Writer adapter scoped to one Run, including all reopen waits
	mu          sync.Mutex
	current     port
	broken      error
	open        portOpener
	name        string
	baud        int
	interactive bool
	connected   bool
	waiting     bool
	retryAt     time.Time
	notify      func(string, ...any) error
}

func (s *Serial) runReconnecting(ctx context.Context, session module.Session, cfg scriptConfig) error {
	if cfg.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}

	serialPort := &reconnectingPort{ctx: ctx, open: s.open, name: s.Port, baud: s.Baud, interactive: cfg.interactive}
	if serialPort.open == nil {
		serialPort.open = defaultOpenPort
	}
	defer serialPort.Close()

	if cfg.interactive {
		con := session.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})
		serialPort.notify = func(format string, args ...any) error {
			_, err := fmt.Fprintf(con.Stderr, "\r\n"+format+"\r\n", args...)

			return err
		}

		return bridgeConsole(ctx, serialPort, newInbound(s.imap, false), con, s.drainFor())
	}

	clientOut := newClientWriter(session)
	serialPort.notify = func(format string, args ...any) error {
		clientOut.markerf(format+"\n", args...)

		return nil
	}

	// Reconnect waiting and the final drain both obey the original global deadline.
	return s.runSequence(ctx, ctx, cfg, serialPort, clientOut)
}

// poll tries once per interval; returning quiet reads keeps console EOF/drain responsive too.
func (p *reconnectingPort) poll(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	err = sleepCtx(ctx, min(time.Until(p.retryAt), readTimeout))
	if err != nil {
		return err
	}

	if time.Now().Before(p.retryAt) {
		return nil
	}

	opened, err := p.open(p.name, p.baud)
	if err != nil {
		if !deviceUnavailable(err) {
			return err
		}

		p.retryAt = time.Now().Add(reconnectInterval)

		return p.waitNotice()
	}

	p.mu.Lock()
	p.current = opened
	p.mu.Unlock()
	p.waiting = false

	label := "Connected to"
	if p.connected {
		label = "Serial device reconnected:"
	}

	p.connected = true
	log.FromContext(ctx).Info(fmt.Sprintf("connected to %s at %d baud", p.name, p.baud))

	// Do not flush: the new handle may already contain the next boot's first bytes.
	return p.notify("--- %s %s at %d baud ---", label, p.name, p.baud)
}

func (p *reconnectingPort) waitNotice() error {
	if p.waiting {
		return nil
	}

	p.waiting = true

	text := "--- Waiting for serial device to reconnect: %s ---"
	if p.interactive {
		text += "\r\n--- Input while disconnected is discarded ---"
	}

	return p.notify(text, p.name)
}

func (p *reconnectingPort) Read(buf []byte) (int, error) {
	p.mu.Lock()
	current, broken := p.current, p.broken
	p.mu.Unlock()

	if current == nil {
		err := p.poll(p.ctx)
		if p.ctx.Err() != nil {
			return 0, nil //nolint:nilerr // caller's read loop handles cancellation as success or expect failure
		}

		return 0, err
	}

	var count int

	if broken == nil {
		count, broken = current.Read(buf)
	}

	p.mu.Lock()
	if p.broken != nil {
		broken = p.broken
	}
	p.mu.Unlock()

	if !deviceUnavailable(broken) {
		return count, broken
	}

	return count, p.disconnect()
}

func (p *reconnectingPort) disconnect() error {
	p.mu.Lock()
	_ = p.current.Close()
	p.current = nil
	p.broken = nil
	p.mu.Unlock()
	p.retryAt = time.Now().Add(reconnectInterval)

	err := p.notify("--- Serial device disconnected: %s ---", p.name)
	if err != nil {
		return err
	}

	err = p.waitNotice()
	if err != nil {
		return err
	}

	return errDisconnected
}

// Write drops offline interactive input, including the rest of an interrupted chunk.
// It never retries bytes whose delivery to the old device is uncertain.
func (p *reconnectingPort) Write(data []byte) (int, error) {
	if !p.interactive {
		err := p.awaitConnection()
		if err != nil {
			return 0, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current == nil || p.broken != nil {
		return len(data), nil
	}

	count, err := p.writeChunk(data)
	if p.interactive && deviceUnavailable(err) {
		p.broken = err

		return len(data), nil
	}

	if err != nil && !p.interactive {
		return count, fmt.Errorf("serial write interrupted; delivery uncertain, command was not retried: %w", err)
	}

	return count, err
}

func (p *reconnectingPort) awaitConnection() error {
	for p.current == nil {
		err := p.poll(p.ctx)
		if err != nil {
			return err
		}
	}

	return p.ctx.Err()
}

// A complete input chunk holds mu, including short writes, so no suffix can
// accidentally land on a replacement connection.
func (p *reconnectingPort) writeChunk(data []byte) (int, error) {
	total := 0

	for total < len(data) {
		count, err := p.current.Write(data[total:])
		total += count

		if err != nil {
			return total, err
		}

		if count == 0 {
			return total, io.ErrShortWrite
		}
	}

	return total, nil
}

func (p *reconnectingPort) ResetInputBuffer() error { return nil }

func (p *reconnectingPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current == nil {
		return nil
	}

	err := p.current.Close()
	p.current = nil

	return err
}

func deviceUnavailable(err error) bool {
	var configErr *portConfigurationError
	if errors.As(err, &configErr) {
		return false
	}

	var portErr *serial.PortError
	if errors.As(err, &portErr) {
		return portErr.Code() == serial.PortNotFound || portErr.Code() == serial.PortClosed
	}

	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENODEV) ||
		errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.EIO)
}
