// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package module provides a plugin system for the DUT package.
// Modules are the building blocks of a command and host the actual implementation
// of the steps that are executed on a device-under-test (DUT).
// The core of the plugin system is the Module interface.
package module

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

//nolint:gochecknoglobals
var (
	modules = make(map[string]Record)
	mutex   sync.RWMutex
)

// Module is a building block of a command running on a device-under-test (DUT).
// Implementations of this interface are the actual steps that are executed on a DUT.
//
// Reserved names: "lock", "unlock", and "help" cannot be used as command names
// in a device configuration — they collide with dutctl's command-line dispatch,
// so the dutagent rejects such a config at startup. A module also must not
// expect "help" as the first argument to Run: the dutctl client intercepts it
// as the help keyword and never forwards it.
type Module interface {
	// Help provides usage information.
	// The returned string should contain a description of the module, the supported
	// command line arguments, and any other information required to interact with the module.
	// The returned string should be formatted in a way that it can be displayed to the user.
	//
	// Implementations should consider the module's concrete configuration and potentially
	// return individual help messages based on the configuration. It is not the purpose
	// of this method to provide a generic help message for all possible configurations,
	// but rather usage information for the current configuration.
	Help() string
	// Init is called once when the dutagent service is started.
	// It's a good place to establish connections or allocate resources and check whether
	// the module is configured and functional. It is also called when a command containing this
	// module is called as a dry-run to check the configuration.
	//
	// A non-nil error marks the module as non-functional. The agent aggregates Init
	// errors across all modules rather than failing on the first, and reports them
	// together (at startup, and for a dry-run). The error is treated opaquely.
	//
	// The context carries a logger scoped to this module; obtain it with
	// log.FromContext(ctx). It has no request deadline (Init runs at startup).
	Init(ctx context.Context) error
	// Deinit is called when the module is unloaded by dutagent or an internal error occurs.
	// It is used to clean up any resources that were allocated during the Init phase and
	// shall guarantee a graceful shutdown of the service.
	//
	// Implementations must be safe to call even if Init was never called or failed partway.
	// Init may fail after partially allocating resources that still need cleanup.
	//
	// The context carries a logger scoped to this module; obtain it with log.FromContext(ctx).
	Deinit(ctx context.Context) error
	// Run is the entry point and executes the module with the given arguments.
	//
	// A nil return reports success; a non-nil error aborts the command and is
	// reported to the client. The error is treated opaquely — the framework does
	// not inspect its type — so implementations may return any error. A panic in
	// Run is recovered at the framework boundary and converted into an error, so a
	// misbehaving module cannot crash dutagent; implementations should nonetheless
	// prefer returning errors over panicking.
	//
	// The context carries a logger scoped to this module (log.FromContext) and is
	// cancelled when the command is aborted or the client disconnects.
	Run(ctx context.Context, s Session, args ...string) error
}

// Session provides an environment / a context for a module.
// Via the Session interface, modules can interact with the client during execution.
//
// The Print family and Console are fire-and-forget: they return no error, and a
// failure to deliver output to the client (for example a broken stream) is handled
// out-of-band by the session, which aborts the run rather than reporting the failure
// back to the module. RequestFile and SendFile do return an error; it is reported
// opaquely (no sentinel to match) and typically means the client declined the file
// or the transfer stream failed.
//
// Console, Print, RequestFile and SendFile must be called only from the module's
// Run goroutine.
type Session interface {
	// Print sends a message to the client. Implementations should wrap [fmt.Sprint].
	// The message is displayed in the console or GUI of the client.
	Print(a ...any)
	// Printf sends a formatted message to the client. Implementations should wrap [fmt.Sprintf].
	// The message is displayed in the console or GUI of the client.
	Printf(format string, a ...any)
	// Println sends a message with appended newline to the client. Implementations should wrap [fmt.Sprintln].
	// The message is displayed in the console or GUI of the client.
	Println(a ...any)
	// Console returns the stdin, stdout and stderr streams for the module.
	// It thus indicates to the client that the module may want to interact with the user
	// via standard input and output streams.
	Console() (stdin io.Reader, stdout, stderr io.Writer)
	// RequestFile requests a file from the client.
	// The file is identified by its name and is made available to the module via the returned io.Reader.
	RequestFile(name string) (io.Reader, error)
	// SendFile sends a file to the client.
	SendFile(name string, r io.Reader) error
}

// Record holds the information required to register a module.
type Record struct {
	// ID is the unique identifier of the module.
	// It is used to reference the module in the dutagent configuration.
	ID string
	// New is the factory function that creates a new instance of the module.
	// Most of the time, this function will return a pointer to a newly allocated struct
	// that implements the Module interface. It is not supposed to run initialization code
	// with side effects. The actual initialization should be done in the Init method of the Module.
	// Instead the factory function may serve as a constructor for the module and can be used to
	// allocate internal resources, like maps and slices or set up the initial state of the module.
	New func() Module
}

// Register registers a module for use in dutagent. It is meant to be called from a
// module package's init function, so a misuse is a programming error surfaced at
// startup rather than a returned error (an init function cannot return one).
//
// Register panics if r.ID is empty, if r.ID is a reserved name ("help" or "info"),
// if r.New is nil, or if a module with the same ID is already registered.
func Register(r Record) {
	if r.ID == "" {
		panic("module ID missing")
	}

	if r.ID == "help" || r.ID == "info" {
		panic(fmt.Sprintf("module ID '%s' is reserved", r.ID))
	}

	if r.New == nil {
		panic("missing factory function 'New func() Module'")
	}

	mutex.Lock()
	defer mutex.Unlock()

	if _, ok := modules[r.ID]; ok {
		panic(fmt.Sprintf("module already registered: %s", r.ID))
	}

	modules[r.ID] = r
}

// New creates a new instance of a formerly registered module by its unique name.
// It returns an error if name is empty or if no module with that name has been
// registered. Both errors are opaque (no sentinel); callers report them as-is.
func New(name string) (Module, error) {
	if name == "" {
		return nil, errors.New("module name must not be empty")
	}

	mod, ok := modules[name]
	if !ok {
		const helpURL = "https://github.com/BlindspotSoftware/dutctl/blob/main/docs/module_guide.md#registration"

		return nil, fmt.Errorf("module %q not found, maybe not registered, see %s", name, helpURL)
	}

	return mod.New(), nil
}
