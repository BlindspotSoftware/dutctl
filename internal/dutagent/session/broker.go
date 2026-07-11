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
// This concerns communication and data exchange.
type Broker struct {
	once    sync.Once
	stream  Stream
	session backend
	errCh   chan error // closed after all workers complete
	wg      sync.WaitGroup
}

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

		b.wg.Add(numWorkers)
		b.toClient(workerCtx, workerCancel)
		b.fromClient(workerCtx, workerCancel)

		go func() {
			b.wg.Wait()
			close(b.errCh)
		}()
	})

	// Rebinding the stream after first start is ignored by design; a Broker is single-use per Run.
	return &b.session, b.errCh
}

func (b *Broker) toClient(ctx context.Context, cancel context.CancelFunc) {
	// Scope the downstream (agent → client) flow; the worker and its chanio
	// reader inherit it from ctx.
	ctx = log.WithScope(ctx, scopeSessionDownstream)

	go func() {
		defer b.wg.Done()

		l := log.FromContext(ctx)
		l.Debug("worker started")

		err := toClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Surfaced to the RPC layer via errCh, which logs the terminal error.
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
		defer b.wg.Done()

		l := log.FromContext(ctx)
		l.Debug("worker started")

		err := fromClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			// Surfaced to the RPC layer via errCh, which logs the terminal error.
			l.Warn("worker terminated", "err", err)
			b.errCh <- err
		} else {
			l.Debug("worker stopped")
		}
		// Cancel companion regardless of outcome; toClientWorker will exit promptly.
		cancel()
	}()
}
