// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fsm provides a simple but powerful finite state machine implementation.
//
// The design is inspired by Rob Pike's talk "Lexical Scanning in Go".
package fsm

import "context"

// State represents a state in the finite state machine.
// It takes args as a set of arguments and returns the arguments for the next state,
// the next State to run or an error.
//
// Returning a nil State indicates the successful end of the state machine.
type State[T any] func(ctx context.Context, args T) (T, State[T], error)

// Run executed the finite state machine with args of any type and start as the first state.
// It keeps executing the states until the current state is nil. In case a state returns an error,
// the execution stops and the error is returned.
func Run[T any](ctx context.Context, args T, start State[T]) (T, error) {
	var err error

	current := start

	for {
		if ctx.Err() != nil {
			return args, ctx.Err()
		}

		args, current, err = current(ctx, args)
		if err != nil {
			return args, err
		}

		if current == nil {
			return args, nil
		}
	}
}
