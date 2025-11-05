// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dutagent provides utilities for a dutagent service to handel the RPC requests.
package dutagent

import (
	"context"
	"log"
	"sync"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Broker mediates between a module and its environment while the module is executed.
// This concerns communication and data exchange.
type Broker struct {
	once    sync.Once
	stream  Stream
	session session
	errCh   chan error
}

func (b *Broker) init() {
	log.Print("Broker: Initializing")

	b.session.printCh = make(chan string)
	b.session.stdinCh = make(chan []byte)
	b.session.stdoutCh = make(chan []byte)
	b.session.stderrCh = make(chan []byte)
	b.session.fileReqCh = make(chan string)
	b.session.fileCh = make(chan chan []byte)

	b.errCh = make(chan error)
}

// Start initializes the broker and launches its workers. It returns the module session
// for module execution and a channel signaling worker termination or errors.
// Multiple calls are idempotent; subsequent calls return the already initialized session and channel.
//
//nolint:ireturn // returning interface module.Session is intentional for abstraction boundary
func (b *Broker) Start(ctx context.Context, s Stream) (module.Session, <-chan error) {
	b.once.Do(func() {
		b.init()
		b.stream = s
		b.toClient(ctx)
		b.fromClient(ctx)
	})

	// Rebinding the stream after first start is ignored by design; a Broker is single-use per Run.
	return &b.session, b.errCh
}

func (b *Broker) toClient(ctx context.Context) {
	// Start a worker for sending messages that are collected by the session form the module to the client.
	// The worker will return when the module execution is finished (the passed context is done) or when an error occurs
	// during the communication with the client.
	go func() {
		log.Print("Broker: Starting module-to-client worker")

		err := toClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			log.Printf("Broker: module-to-client worker terminated: %v", err)
		} else {
			log.Print("Broker: module-to-client worker returned")
		}
		b.errCh <- err // Signal the main broker routine that the worker has returned or an error occurred.
	}()
}

func (b *Broker) fromClient(ctx context.Context) {
	// Start a worker for sending messages that are collected by the session form the module to the client.
	// The worker will return when the module execution is finished (the passed context is done) or when an error occurs
	// during the communication with the client.
	// In case of a non-interactive module (client does not send further messages), the worker will block forever.
	// and waiting for it will never return.
	// However, if the stream is closed, the receive calls to the client unblock and the worker will return.
	go func() {
		log.Print("Broker: Starting client-to-module worker")

		err := fromClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			log.Printf("Broker: client-to-module worker terminated: %v", err)
			b.errCh <- err // Signal only if an error occurred. See comment above.
		} else {
			log.Print("Broker: client-to-module worker returned")
		}
	}()
}
