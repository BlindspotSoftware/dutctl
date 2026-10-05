// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package session brokers module<->client communication during a Run: it adapts
// the RPC stream into a module.Session and runs the workers that carry the traffic
// in both directions.
package session

import (
	"context"
	"sync"

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
// This concerns communication and data exchange. A Broker is single-use: call
// Start once, and Stop when the modules are done.
type Broker struct {
	stream  Stream
	session backend
	wg      sync.WaitGroup

	stop   context.CancelFunc      // stops the workers; nil until Start
	cancel context.CancelCauseFunc // cancels the context Start returned

	errOnce sync.Once
	err     error // the first worker failure; read only after wg.Wait
}

func (b *Broker) init() {
	b.session.printCh = make(chan string)
	b.session.stdinCh = make(chan []byte)
	b.session.stdoutCh = make(chan []byte)
	b.session.stderrCh = make(chan []byte)
	b.session.fileReqCh = make(chan string)
	b.session.fileCh = make(chan chan []byte)
	b.session.uploadCh = make(chan chan []byte)
}

// Start launches the broker's workers, which carry the returned module
// session's I/O over s until Stop is called or ctx is done.
//
// The returned context, derived from ctx, is the one to run the modules under.
// Like the one errgroup.WithContext returns, it is cancelled when the first
// worker fails, with the failure as its cause: a worker fails only when the
// stream is broken or the client breaks the protocol, and either way the modules
// must stop. Otherwise it is cancelled when Stop returns. A worker that ends
// cleanly, because the client closed its side, does not cancel it.
func (b *Broker) Start(ctx context.Context, s Stream) (module.Session, context.Context) {
	runCtx, cancel := context.WithCancelCause(ctx)
	b.cancel = cancel

	b.init()
	b.stream = s

	ctx = log.WithScope(runCtx, scopeSession)
	// Freeze the session-scoped logger onto the session: its module-facing
	// methods carry no context to derive a logger from.
	b.session.log = log.FromContext(ctx)

	log.FromContext(ctx).Debug("broker initializing")

	workerCtx, stopWorkers := context.WithCancel(ctx)
	b.stop = stopWorkers
	// Freeze the workers' done signal onto the session so the module-facing
	// methods (which carry no context) can abort a channel op whose worker
	// peer has exited. Set here, before the workers start and before Start
	// returns to the caller that later runs the modules, so there is no race
	// on the read.
	b.session.done = workerCtx.Done()

	b.wg.Add(numWorkers)
	b.toClient(workerCtx)
	b.fromClient(workerCtx)

	return &b.session, runCtx
}

// Stop stops the workers and waits for them to return, so that no stream Send
// is still in progress once it returns. It returns the first worker failure, or
// nil. It returns nil at once if the Broker was never started, and may be called
// more than once. Call it from the goroutine that called Start, or after Start
// returned.
//
// The RPC handler relies on this: a stream Send running past the handler's return
// panics inside net/http, in a worker goroutine no recover covers. The upstream
// worker's receive goroutine is not awaited: it only reads the request body,
// which the transport closes once the handler returns, so it ends then.
func (b *Broker) Stop() error {
	if b.stop == nil {
		return nil
	}

	b.stop()
	b.wg.Wait()
	b.cancel(nil)

	return b.err
}

// fail records the first worker failure and cancels the modules' context with
// it, in one step, so the error Stop returns and the cancellation cause are the
// same failure.
func (b *Broker) fail(err error) {
	b.errOnce.Do(func() {
		b.err = err
		b.cancel(err)
	})
}

func (b *Broker) toClient(ctx context.Context) {
	// Scope the downstream (agent → client) flow; the worker and its chanio
	// reader inherit it from ctx.
	ctx = log.WithScope(ctx, scopeSessionDownstream)

	go func() {
		defer b.wg.Done()

		l := log.FromContext(ctx)
		l.Debug("worker started")

		err := toClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Log the worker's terminal failure at session scope, and surface it to
			// the RPC layer via fail and Stop for request classification. This is
			// the sanctioned detail+summary double-log: the RPC handler (Run) also
			// logs the rpc-scope summary of the returned error.
			l.Warn("worker terminated", "err", err)
			b.fail(err)
		} else {
			l.Debug("worker stopped")
		}
		// Stop the companion regardless of outcome; fromClientWorker drains one pending receive to catch concurrent error.
		b.stop()
	}()
}

func (b *Broker) fromClient(ctx context.Context) {
	// Scope the upstream (client → agent) flow; the worker inherits it from ctx.
	ctx = log.WithScope(ctx, scopeSessionUpstream)

	go func() {
		defer b.wg.Done()

		l := log.FromContext(ctx)
		l.Debug("worker started")

		err := fromClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Log the worker's terminal failure at session scope, and surface it to
			// the RPC layer via fail and Stop for request classification. This is
			// the sanctioned detail+summary double-log: the RPC handler (Run) also
			// logs the rpc-scope summary of the returned error.
			l.Warn("worker terminated", "err", err)
			b.fail(err)
		} else {
			l.Debug("worker stopped")
		}
		// Stop the companion regardless of outcome; toClientWorker will exit promptly.
		b.stop()
	}()
}
