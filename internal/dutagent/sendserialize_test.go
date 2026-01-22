// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// concurrencyStream records the maximum number of Send calls that overlap.
// The connect BidiStream forbids concurrent Send, so sendToClient must keep
// this at 1 no matter how many goroutines call it.
type concurrencyStream struct {
	inSend  atomic.Int32
	maxSeen atomic.Int32
	calls   atomic.Int32
}

func (c *concurrencyStream) Send(_ *pb.RunResponse) error {
	now := c.inSend.Add(1)

	for {
		prev := c.maxSeen.Load()
		if now <= prev || c.maxSeen.CompareAndSwap(prev, now) {
			break
		}
	}

	time.Sleep(time.Millisecond) // widen the window so overlaps are observable

	c.calls.Add(1)
	c.inSend.Add(-1)

	return nil
}

func (c *concurrencyStream) Receive() (*pb.RunRequest, error) { return nil, io.EOF }

// TestSendToClientSerializes verifies that sendToClient serialises stream sends,
// which is required because both workers send on the same connect BidiStream.
func TestSendToClientSerializes(t *testing.T) {
	s := &session{}
	stream := &concurrencyStream{}

	const goroutines = 50

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()

			_ = s.sendToClient(stream, &pb.RunResponse{})
		}()
	}

	wg.Wait()

	if got := stream.maxSeen.Load(); got > 1 {
		t.Errorf("observed %d concurrent Send calls, want at most 1", got)
	}

	if got := stream.calls.Load(); got != goroutines {
		t.Errorf("Send called %d times, want %d", got, goroutines)
	}
}
