// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
)

// commandKeywordHandler dispatches a reserved keyword that appears after
// a <device> <command> pair (e.g. "<device> <cmd> help").
type commandKeywordHandler func(app *application, device, command string) error

// deviceKeywordHandler dispatches a reserved keyword that appears in
// the device position (e.g. "help" in "dutctl help").
type deviceKeywordHandler func(app *application, name string) error

//nolint:gochecknoglobals // dispatch table keyed by dut.CommandKeywords.
var commandKeywordHandlers = map[string]commandKeywordHandler{
	"help": func(app *application, device, command string) error {
		return app.detailsRPC(device, command, "help")
	},
}

//nolint:gochecknoglobals // dispatch table keyed by dut.DeviceKeywords.
var deviceKeywordHandlers = map[string]deviceKeywordHandler{
	"help": func(app *application, _ string) error {
		fmt.Fprint(app.stderr, usageAbstract, usageSynopsis, usageDescription)
		app.printFlagDefaults()

		return nil
	},
}

// init enforces that every name in the dut keyword registry has a
// matching handler. Missing one is a programmer error caught at
// startup rather than at request time.
func init() {
	for _, kw := range dut.CommandKeywords {
		if _, ok := commandKeywordHandlers[kw.Name]; !ok {
			panic("dutctl: missing handler for command keyword " + kw.Name)
		}
	}

	for _, kw := range dut.DeviceKeywords {
		if _, ok := deviceKeywordHandlers[kw.Name]; !ok {
			panic("dutctl: missing handler for device keyword " + kw.Name)
		}
	}
}
