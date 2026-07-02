// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"context"
	"fmt"
	"log"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
)

// catchPanic calls fn and recovers from any panic, returning it as an error.
// This intentionally does NOT re-panic. A module panic is recorded as an error
// so the Init/Deinit loop can continue with the remaining modules.
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

// ModuleInitError is a container for errors that occur during module
// initialization.
type ModuleInitError struct {
	Errs []ModuleInitErrorDetails
	msg  string
}

func (e *ModuleInitError) Error() string {
	if len(e.Errs) == 1 {
		return fmt.Sprintf("%s - %d problem", e.msg, len(e.Errs))
	}

	return fmt.Sprintf("%s - %d problems", e.msg, len(e.Errs))
}

type ModuleInitErrorDetails struct {
	Dev string
	Cmd string
	Mod dut.Module
	Err error
}

// Init runs the Init function of all modules for all commands of the provided
// devices. All init functions are called, even if an error occurs. In this case
// the an ModuleInitErr is returned that holds all errors reported by the modules.
//
// ctx is the agent-lifetime context for startup and is passed to every module's
// Init. It is a plain background context today — see the caller for where a
// startup deadline would attach.
func Init(ctx context.Context, devices dut.Devlist) error {
	var ierr = &ModuleInitError{
		Errs: make([]ModuleInitErrorDetails, 0),
		msg:  "module initialization failed",
	}

	for devname, device := range devices {
		for cmdname, cmd := range device.Cmds {
			for _, module := range cmd.Modules {
				err := catchPanic(func() error { return module.Init(ctx) })
				if err != nil {
					ierr.Errs = append(ierr.Errs, ModuleInitErrorDetails{
						Dev: devname,
						Cmd: cmdname,
						Mod: module,
						Err: err,
					})
				}
			}
		}
	}

	if len(ierr.Errs) != 0 {
		return ierr
	}

	log.Print("Module Initialization OK")

	return nil
}

// Deinit runs the Deinit function of all modules for all commands of the provided
// devices. All Deinit functions are called, even if an error occurs. In this case
// the an ModuleInitErr is returned that holds all errors reported by the modules.
//
// ctx is the shutdown context and is passed to every module's Deinit. It is a
// plain background context today — see the caller for where a shutdown deadline
// would attach.
func Deinit(ctx context.Context, devices dut.Devlist) error {
	var derr = &ModuleInitError{
		Errs: make([]ModuleInitErrorDetails, 0),
		msg:  "bad clean-up",
	}

	log.Printf("GRACEFUL SHUTDOWN: De-init modules")

	for devname, device := range devices {
		for cmdname, cmd := range device.Cmds {
			for _, module := range cmd.Modules {
				err := catchPanic(func() error { return module.Deinit(ctx) })
				if err != nil {
					derr.Errs = append(derr.Errs, ModuleInitErrorDetails{
						Dev: devname,
						Cmd: cmdname,
						Mod: module,
						Err: err,
					})
				}
			}
		}
	}

	if len(derr.Errs) != 0 {
		return derr
	}

	log.Print("All modules de-initialized")

	return nil
}
