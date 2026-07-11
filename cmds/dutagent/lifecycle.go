// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
)

// catchPanic calls fn and recovers from any panic, returning it as an error.
// This intentionally does NOT re-panic. A module panic is recorded as an error
// so the init/deinit loop can continue with the remaining modules.
func catchPanic(fn func() error) error {
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()

		err = fn()
	}()

	return err
}

// moduleInitError is a container for errors that occur during module
// initialization or de-initialization.
type moduleInitError struct {
	Errs []moduleInitErrorDetails
	msg  string
}

func (e *moduleInitError) Error() string {
	if len(e.Errs) == 1 {
		return fmt.Sprintf("%s - %d problem", e.msg, len(e.Errs))
	}

	return fmt.Sprintf("%s - %d problems", e.msg, len(e.Errs))
}

type moduleInitErrorDetails struct {
	Dev string
	Cmd string
	Mod dut.Module
	Err error
}

// initModules runs the Init function of every module across all devices and
// commands. All Init functions are called, even if one fails; in that case a
// *moduleInitError aggregating every reported error is returned.
//
// ctx is the agent-lifetime context for startup; each module's Init receives a
// child of it carrying the module-scoped logger. It is a plain background
// context today — see the caller for where a startup deadline would attach.
func initModules(ctx context.Context, devices dut.Devlist) error {
	var ierr = &moduleInitError{
		Errs: make([]moduleInitErrorDetails, 0),
		msg:  "module initialization failed",
	}

	for ref := range devices.AllModules() {
		mlog := log.Scope(log.FromContext(ctx), "module").With("device", ref.Device, "command", ref.Command, "module", ref.Module.Config.Name)
		mctx := log.Into(ctx, mlog)

		mlog.Debug("initializing module")

		err := catchPanic(func() error { return ref.Module.Init(mctx) })
		if err != nil {
			// Aggregated and reported by the caller (printInitErr); not logged here.
			ierr.Errs = append(ierr.Errs, moduleInitErrorDetails{
				Dev: ref.Device,
				Cmd: ref.Command,
				Mod: ref.Module,
				Err: err,
			})
		}
	}

	if len(ierr.Errs) != 0 {
		return ierr
	}

	log.FromContext(ctx).Info("module initialization complete")

	return nil
}

// deinitModules runs the Deinit function of every module across all devices and
// commands. All Deinit functions are called, even if one fails; in that case a
// *moduleInitError aggregating every reported error is returned.
//
// ctx is the shutdown context; each module's Deinit receives a child of it
// carrying the module-scoped logger. It is a plain background context today —
// see the caller for where a shutdown deadline would attach.
func deinitModules(ctx context.Context, devices dut.Devlist) error {
	var derr = &moduleInitError{
		Errs: make([]moduleInitErrorDetails, 0),
		msg:  "bad clean-up",
	}

	log.FromContext(ctx).Info("graceful shutdown: deinitializing modules")

	for ref := range devices.AllModules() {
		mlog := log.Scope(log.FromContext(ctx), "module").With("device", ref.Device, "command", ref.Command, "module", ref.Module.Config.Name)
		mctx := log.Into(ctx, mlog)

		mlog.Debug("deinitializing module")

		err := catchPanic(func() error { return ref.Module.Deinit(mctx) })
		if err != nil {
			derr.Errs = append(derr.Errs, moduleInitErrorDetails{
				Dev: ref.Device,
				Cmd: ref.Command,
				Mod: ref.Module,
				Err: err,
			})
		}
	}

	if len(derr.Errs) != 0 {
		return derr
	}

	log.FromContext(ctx).Info("all modules deinitialized")

	return nil
}
