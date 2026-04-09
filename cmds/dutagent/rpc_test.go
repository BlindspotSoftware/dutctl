// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func newTestService() *rpcService {
	return &rpcService{
		devices: dut.Devlist{"dev1": {}, "dev2": {}},
		locker:  dutagent.NewLocker(),
	}
}

func TestLockRPC(t *testing.T) {
	tests := []struct {
		name        string
		device      string
		owner       string
		duration    int64
		setup       func(*rpcService)
		wantErrCode connect.Code
		wantErr     bool
	}{
		{
			name:     "happy path — locks device",
			device:   "dev1",
			owner:    "alice@host",
			duration: 300,
		},
		{
			name:        "unknown device — CodeInvalidArgument",
			device:      "unknown",
			owner:       "alice@host",
			duration:    300,
			wantErr:     true,
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:     "already locked by different owner — CodeFailedPrecondition",
			device:   "dev1",
			owner:    "alice@host",
			duration: 300,
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "bob@host", time.Minute)
			},
			wantErr:     true,
			wantErrCode: connect.CodeFailedPrecondition,
		},
		{
			name:     "same owner re-lock — extends lock",
			device:   "dev1",
			owner:    "alice@host",
			duration: 300,
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "alice@host", time.Minute)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			if tt.setup != nil {
				tt.setup(svc)
			}

			req := connect.NewRequest(&pb.LockRequest{
				Device:          tt.device,
				Owner:           tt.owner,
				DurationSeconds: tt.duration,
			})

			res, err := svc.Lock(context.Background(), req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.wantErrCode {
					t.Fatalf("expected connect error code %v, got %v (err=%v)", tt.wantErrCode, cErr.Code(), err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if res.Msg.GetOwner() != tt.owner {
				t.Errorf("owner = %q, want %q", res.Msg.GetOwner(), tt.owner)
			}

			if res.Msg.GetExpiresAt() <= res.Msg.GetLockedAt() {
				t.Errorf("ExpiresAt (%d) should be after LockedAt (%d)", res.Msg.GetExpiresAt(), res.Msg.GetLockedAt())
			}
		})
	}
}

func TestUnlockRPC(t *testing.T) {
	tests := []struct {
		name        string
		device      string
		owner       string
		setup       func(*rpcService)
		wantErrCode connect.Code
		wantErr     bool
	}{
		{
			name:   "happy path — unlocks device",
			device: "dev1",
			owner:  "alice@host",
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "alice@host", time.Minute)
			},
		},
		{
			name:        "unknown device — CodeInvalidArgument",
			device:      "unknown",
			owner:       "alice@host",
			wantErr:     true,
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:   "wrong owner — CodeFailedPrecondition",
			device: "dev1",
			owner:  "alice@host",
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "bob@host", time.Minute)
			},
			wantErr:     true,
			wantErrCode: connect.CodeFailedPrecondition,
		},
		{
			name:        "not locked — CodeFailedPrecondition",
			device:      "dev1",
			owner:       "alice@host",
			wantErr:     true,
			wantErrCode: connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			if tt.setup != nil {
				tt.setup(svc)
			}

			req := connect.NewRequest(&pb.UnlockRequest{
				Device: tt.device,
				Owner:  tt.owner,
			})

			_, err := svc.Unlock(context.Background(), req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.wantErrCode {
					t.Fatalf("expected connect error code %v, got %v (err=%v)", tt.wantErrCode, cErr.Code(), err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLockStatusRPC(t *testing.T) {
	tests := []struct {
		name        string
		device      string
		setup       func(*rpcService)
		wantLocked  bool
		wantOwner   string
		wantCount   int
		wantErrCode connect.Code
		wantErr     bool
	}{
		{
			name:   "specific device — locked",
			device: "dev1",
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "alice@host", time.Minute)
			},
			wantLocked: true,
			wantOwner:  "alice@host",
			wantCount:  1,
		},
		{
			name:       "specific device — unlocked",
			device:     "dev1",
			wantLocked: false,
			wantCount:  1,
		},
		{
			name:        "unknown device — CodeInvalidArgument",
			device:      "unknown",
			wantErr:     true,
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:      "all devices — empty device field",
			device:    "",
			wantCount: 2, // dev1 + dev2
		},
		{
			name:   "all devices — one locked",
			device: "",
			setup: func(s *rpcService) {
				_, _ = s.locker.Lock("dev1", "ci@runner", time.Minute)
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			if tt.setup != nil {
				tt.setup(svc)
			}

			req := connect.NewRequest(&pb.LockStatusRequest{Device: tt.device})
			res, err := svc.LockStatus(context.Background(), req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.wantErrCode {
					t.Fatalf("expected connect error code %v, got %v (err=%v)", tt.wantErrCode, cErr.Code(), err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			locks := res.Msg.GetLocks()
			if len(locks) != tt.wantCount {
				t.Fatalf("lock count = %d, want %d", len(locks), tt.wantCount)
			}

			if tt.device != "" && tt.wantCount == 1 {
				entry := locks[0]
				if entry.GetLocked() != tt.wantLocked {
					t.Errorf("locked = %v, want %v", entry.GetLocked(), tt.wantLocked)
				}

				if tt.wantLocked && entry.GetOwner() != tt.wantOwner {
					t.Errorf("owner = %q, want %q", entry.GetOwner(), tt.wantOwner)
				}
			}
		})
	}
}

func TestLockInfoProto(t *testing.T) {
	t.Run("ordering matches devices slice", func(t *testing.T) {
		now := time.Now()
		locks := map[string]dutagent.LockInfo{
			"dev2": {Owner: "bob", LockedAt: now, ExpiresAt: now.Add(time.Minute)},
		}
		devices := []string{"dev1", "dev2", "dev3"}

		result := lockInfoProto(devices, locks)

		if len(result) != 3 {
			t.Fatalf("len = %d, want 3", len(result))
		}

		if result[0].GetDevice() != "dev1" || result[0].GetLocked() {
			t.Errorf("dev1: expected unlocked, got %+v", result[0])
		}

		if result[1].GetDevice() != "dev2" || !result[1].GetLocked() || result[1].GetOwner() != "bob" {
			t.Errorf("dev2: expected locked by bob, got %+v", result[1])
		}

		if result[2].GetDevice() != "dev3" || result[2].GetLocked() {
			t.Errorf("dev3: expected unlocked, got %+v", result[2])
		}
	})

	t.Run("empty locks map", func(t *testing.T) {
		result := lockInfoProto([]string{"dev1"}, map[string]dutagent.LockInfo{})

		if len(result) != 1 || result[0].GetLocked() {
			t.Errorf("expected single unlocked entry, got %+v", result)
		}
	})
}
