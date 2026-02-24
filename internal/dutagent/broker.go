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

// numWorkers is the number of broker workers. One worker handles module-to-client communication,
// the other handles client-to-module communication.
const numWorkers = 2

// Broker mediates between a module and its environment while the module is executed.
// This concerns communication and data exchange.
type Broker struct {
	once    sync.Once
	stream  Stream
	session session
	errCh   chan error // closed after all workers complete
	wg      sync.WaitGroup
}

func (b *Broker) init() {
	log.Print("Broker: Initializing")

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

func (b *Broker) Start(ctx context.Context, s Stream) (module.Session, <-chan error) {
	b.once.Do(func() {
		b.init()
		b.stream = s

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
	go func() {
		defer b.wg.Done()

		log.Print("Broker: Starting module-to-client worker")

		err := toClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			log.Printf("Broker: module-to-client worker terminated: %v", err)
			b.errCh <- err
		} else {
			log.Print("Broker: module-to-client worker returned")
		}
		// Cancel companion regardless of outcome; fromClientWorker drains one pending receive to catch concurrent error.
		cancel()
	}()
}

func (b *Broker) fromClient(ctx context.Context, cancel context.CancelFunc) {
	go func() {
		defer b.wg.Done()

		log.Print("Broker: Starting client-to-module worker")

		err := fromClientWorker(ctx, b.stream, &b.session)
		if err != nil {
			log.Printf("Broker: client-to-module worker terminated: %v", err)
			b.errCh <- err
		} else {
			log.Print("Broker: client-to-module worker returned")
		}
		// Cancel companion regardless of outcome; toClientWorker will exit promptly.
		cancel()
	}()
}
