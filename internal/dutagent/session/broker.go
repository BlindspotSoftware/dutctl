// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package session brokers module<->client communication during a Run: it adapts
// the RPC stream into a module.Session and runs the workers that carry the traffic
// in both directions.
package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// numWorkers is the number of broker workers. One worker handles module-to-client communication,
// the other handles client-to-module communication.
const numWorkers = 2

// Session log scopes. The general scope covers broker setup; the directional
// scopes distinguish the two communication flows, and are inherited by the
// workers and the chanio readers they (and the session) construct.
const (
	scopeSession           = "session"            // general session/broker setup
	scopeSessionDownstream = "session downstream" // agent/session → client
	scopeSessionUpstream   = "session upstream"   // client → agent/session
)

// Broker mediates between a module and its environment while the module is executed.
// This concerns communication and data exchange.
type Broker struct {
	// stopTimeout overrides how long Stop waits for the workers; zero uses
	// defaultStopTimeout. It exists so tests can shorten the wait; production
	// leaves it 0.
	stopTimeout time.Duration

	once    sync.Once
	stream  Stream
	session backend
	errCh   chan error // closed after all workers complete
	wg      sync.WaitGroup

	// mu guards the teardown handles below. Start writes them, Stop reads them,
	// and while the RPC handler does both from its own goroutine, nothing in the
	// type's contract says a caller has to.
	mu      sync.Mutex
	cancel  context.CancelFunc // stops the workers; nil until Start ran
	stopped chan struct{}      // closed after all workers returned
}

// defaultStopTimeout bounds how long Stop waits for the workers. A worker stuck
// in a stream send past this window is abandoned: blocking the RPC handler
// forever is the worse outcome.
const defaultStopTimeout = 5 * time.Second

func (b *Broker) init() {
	b.session.printCh = make(chan string)
	b.session.stdinCh = make(chan []byte)
	b.session.stdoutCh = make(chan []byte)
	b.session.stderrCh = make(chan []byte)
	b.session.fileReqCh = make(chan string)
	b.session.fileCh = make(chan chan []byte)

	// Buffer equals number of workers so error sends never block.
	b.errCh = make(chan error, numWorkers)
}

// Start initializes the broker and launches its workers. It returns the module session
// for module execution and a channel signaling worker termination or errors.
// Multiple calls are idempotent; subsequent calls return the already initialized session and channel.
//
// The returned channel is error-only and carries at most one error per worker; a
// nil error is never sent. It is closed once both workers have finished, so a
// receiver can drain any errors and then observe closure to know the session has
// fully stopped.
func (b *Broker) Start(ctx context.Context, s Stream) (module.Session, <-chan error) {
	ctx = log.WithScope(ctx, scopeSession)

	b.once.Do(func() {
		b.init()
		b.stream = s
		// Freeze the session-scoped logger onto the session: its module-facing
		// methods carry no context to derive a logger from.
		b.session.log = log.FromContext(ctx)

		log.FromContext(ctx).Debug("broker initializing")

		workerCtx, workerCancel := context.WithCancel(ctx)
		stopped := make(chan struct{})

		b.mu.Lock()
		b.cancel = workerCancel
		b.stopped = stopped
		b.mu.Unlock()
		// Freeze the workers' done signal onto the session so the module-facing
		// methods (which carry no context) can abort a channel op whose worker
		// peer has exited. Set here, before the workers start and before Start
		// returns to the caller that later spawns the module goroutine, so there
		// is no race on the read.
		b.session.done = workerCtx.Done()

		b.wg.Add(numWorkers)
		b.toClient(workerCtx, workerCancel)
		b.fromClient(workerCtx, workerCancel)

		go func() {
			b.wg.Wait()
			// Both closes mean the same thing - every worker has returned from its
			// last stream call - so their order is not load-bearing; stopped is
			// closed first only to keep Stop's signal ahead of the one the RPC
			// layer drains.
			close(stopped)
			close(b.errCh)
		}()
	})

	// Rebinding the stream after first start is ignored by design; a Broker is single-use per Run.
	return &b.session, b.errCh
}

// Stop cancels the workers and waits for them to return. Calling it on a Broker
// that was never started, or calling it twice, is a no-op.
//
// The RPC handler must not return while a worker is still inside a stream send.
// connect invalidates the response writer once the handler is gone, and a send
// running past that point does not fail - it panics with "Write called after
// Handler finished" in the worker goroutine. net/http recovers a panic in the
// handler itself, but not in a goroutine the handler left behind, so the panic
// takes the whole agent down. A module that prints right before it fails (the
// flash module forwards the flash tool's output, then returns the tool's exit
// error) hits that window on every failed run.
//
// A worker stuck in a send past the stop timeout is abandoned, so the panic is
// not structurally impossible - it is bounded to a genuinely hung transport
// write, where the alternative is a handler that never returns. The workers
// recover from it (see toClient/fromClient) so the agent survives that case too.
func (b *Broker) Stop(ctx context.Context) {
	b.mu.Lock()
	cancel, stopped := b.cancel, b.stopped
	b.mu.Unlock()

	if cancel == nil {
		return // never started
	}

	cancel()

	stopTimeout := b.stopTimeout
	if stopTimeout == 0 {
		stopTimeout = defaultStopTimeout
	}

	timeout := time.NewTimer(stopTimeout)
	defer timeout.Stop()

	select {
	case <-stopped:
	case <-timeout.C:
		log.FromContext(ctx).Warn("workers did not stop in time", "timeout", stopTimeout)
	}
}

// recoverWorker turns a panic in a worker goroutine into a failed run and stops
// the companion worker. A worker panics where its own recover cannot help it: a
// stream send that outlives the RPC handler panics inside net/http, and the
// goroutine sits outside the handler, so an unrecovered panic there takes the
// whole agent down - including every other run in flight. Stop keeps that send
// from outliving the handler in the first place; this is the net for a worker it
// had to abandon at its timeout.
//
// The panic is reported on errCh like any other worker failure, so the run fails
// instead of looking like a broker that finished cleanly. The send neither blocks
// nor hits a closed channel: errCh holds one slot per worker, a worker reports
// either a panic or a terminal error but never both, and errCh is closed only
// once both workers have returned - which is after this recover.
func (b *Broker) recoverWorker(l *slog.Logger, cancel context.CancelFunc) {
	r := recover()
	if r == nil {
		return
	}

	l.Error("worker panicked", "err", r)

	select {
	case b.errCh <- fmt.Errorf("session worker panicked: %v", r):
	default:
	}

	cancel()
}

func (b *Broker) toClient(ctx context.Context, cancel context.CancelFunc) {
	// Scope the downstream (agent → client) flow; the worker and its chanio
	// reader inherit it from ctx.
	ctx = log.WithScope(ctx, scopeSessionDownstream)

	go func() {
		l := log.FromContext(ctx)

		defer b.wg.Done()
		defer b.recoverWorker(l, cancel)

		l.Debug("worker started")

		err := toClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Log the worker's terminal failure at session scope, and surface it to
			// the RPC layer via errCh for request classification. This is the
			// sanctioned detail+summary double-log: the RPC handler (Run) also logs
			// the rpc-scope summary of the returned error.
			l.Warn("worker terminated", "err", err)
			b.errCh <- err
		} else {
			l.Debug("worker stopped")
		}
		// Cancel companion regardless of outcome; fromClientWorker drains one pending receive to catch concurrent error.
		cancel()
	}()
}

func (b *Broker) fromClient(ctx context.Context, cancel context.CancelFunc) {
	// Scope the upstream (client → agent) flow; the worker inherits it from ctx.
	ctx = log.WithScope(ctx, scopeSessionUpstream)

	go func() {
		l := log.FromContext(ctx)

		defer b.wg.Done()
		defer b.recoverWorker(l, cancel)

		l.Debug("worker started")

		err := fromClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Log the worker's terminal failure at session scope, and surface it to
			// the RPC layer via errCh for request classification. This is the
			// sanctioned detail+summary double-log: the RPC handler (Run) also logs
			// the rpc-scope summary of the returned error.
			l.Warn("worker terminated", "err", err)
			b.errCh <- err
		} else {
			l.Debug("worker stopped")
		}
		// Cancel companion regardless of outcome; toClientWorker will exit promptly.
		cancel()
	}()
}
