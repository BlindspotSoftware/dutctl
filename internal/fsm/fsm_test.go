// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package fsm

import (
	"context"
	"errors"
	"testing"
)

// Mock state that increments the argument by 1 and has no next state.
func stateOne(ctx context.Context, args int) (int, State[int], error) {
	return args + 1, nil, nil
}

// Mock state that increments the argument by 2 and moves to stateOne.
func stateTwo(ctx context.Context, args int) (int, State[int], error) {
	return args + 2, stateOne, nil
}

// Mock state that returns an error.
func errorState(ctx context.Context, args int) (int, State[int], error) {
	return args, nil, errors.New("error occurred")
}

func TestRun_OneState(t *testing.T) {
	result, err := Run(context.Background(), 0, stateOne)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != 1 {
		t.Fatalf("expected result to be 1, got %v", result)
	}
}

func TestRun_MultipleStates(t *testing.T) {
	result, err := Run(context.Background(), 0, stateTwo)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != 3 {
		t.Fatalf("expected result to be 3, got %v", result)
	}
}

func TestRun_ErrorState(t *testing.T) {
	result, err := Run(context.Background(), 0, errorState)
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if result != 0 {
		t.Fatalf("expected result to be 0, got %v", result)
	}
}
