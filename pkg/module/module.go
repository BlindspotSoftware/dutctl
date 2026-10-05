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
	// log.FromContext(ctx). It is done 5 minutes after the agent began to
	// initialize the modules, or earlier when the agent is asked to stop during
	// startup. Init should then return promptly: the agent's startup, and with it
	// an orderly stop, waits for it.
	Init(ctx context.Context) error
	// Deinit is called when the module is unloaded by dutagent or an internal error occurs.
	// It is used to clean up any resources that were allocated during the Init phase and
	// shall guarantee a graceful shutdown of the service.
	//
	// Implementations must be safe to call even if Init was never called or failed partway.
	// Init may fail after partially allocating resources that still need cleanup.
	//
	// dutagent calls Deinit only once no command runs any more, so never concurrently
	// with Run. When the agent is killed, or exits at once on a stop signal while it
	// aborts its commands, Deinit is not called at all; the next Init then finds the
	// hardware as it was left.
	//
	// The context carries a logger scoped to this module; obtain it with log.FromContext(ctx).
	// It is done 15 seconds after deinitialization began; the agent then exits without
	// waiting for the modules that have not returned.
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
	// cancelled when the command is aborted or the client disconnects. Run must
	// then return promptly: until it does, its Run request stays open and the
	// command's device stays busy for every other command, the same user's
	// included. A forced unlock does not free it.
	Run(ctx context.Context, s Session, args ...string) error
}

// Session provides an environment / a context for a module.
// Via the Session interface, modules can interact with the client during execution.
//
// The Print family and OpenConsole are fire-and-forget: they return no error, and a
// failure to deliver output to the client (for example a broken stream) is handled
// out-of-band by the session, which aborts the run rather than reporting the failure
// back to the module. RequestFile and SendFile do return an error; it is reported
// opaquely (no sentinel to match) and typically means the client declined the file
// or the transfer stream failed.
//
// OpenConsole, Print, RequestFile and SendFile must be called only from the module's
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
	// OpenConsole opens a console for the module and returns its streams: the
	// user's input on Stdin, and Stdout and Stderr, which reach the user's
	// standard output and standard error. It tells the client the console's
	// mode, so the client can set up the user's terminal, and the client
	// forwards the user's input from then on; until a console is open, the
	// user's input is discarded, never queued for a later console. Like
	// Print, OpenConsole returns once the client was told, so it waits for a
	// stalled client.
	//
	// Two things happen to a console that the module does not control, and
	// both are the normal course of a run, not a failure:
	//
	//   - The user's input ends: a pipe is at its end, or the user pressed
	//     Ctrl-D in a line console. Stdin then returns io.EOF, while Stdout
	//     and Stderr keep working, so the module can still show the far end's
	//     last output.
	//   - The session is torn down: the client disconnected or the run was
	//     aborted. Stdin then returns io.EOF, and a write fails with
	//     io.ErrClosedPipe. OpenConsole itself returns at once with such a
	//     console after a teardown.
	//
	// What a module has to do in turn:
	//
	//   - Keep reading Stdin, or close it when it needs no more input: while
	//     a console is open, the user's input waits for the module to read it,
	//     and so does everything the client sends after it, a requested file
	//     for instance. Stdin.Close ends the input from any goroutine, also
	//     while a Read blocks, and leaves the output open.
	//   - Take io.EOF on Stdin and io.ErrClosedPipe on a write as the end of
	//     the console and return nil, unless something else went wrong.
	//   - Stop every goroutine that uses the console before Run returns: the
	//     console closes when Run returns.
	//
	// A module has one console at a time: a second OpenConsole closes the
	// first, whose Stdin then reports io.EOF and whose writers fail. Print may
	// be used while a console is open.
	OpenConsole(opts ConsoleOptions) Console
	// RequestFile requests a file from the client.
	// The file is identified by its name and is made available to the module via the returned io.Reader.
	RequestFile(name string) (io.Reader, error)
	// SendFile sends a file to the client.
	SendFile(name string, r io.Reader) error
}

// ConsoleMode is how a module uses its console. Only the module knows, and the
// client sets up the user's terminal by it, so a module states the mode it
// needs when it opens a console.
type ConsoleMode int

const (
	// ConsoleLine is a console of text lines, for prompts and other
	// question-and-answer interaction, and for input piped to a process. The
	// user's terminal keeps its line editing and local echo, Enter sends the
	// line, and Ctrl-C interrupts the client and ends the run.
	//
	// The module gets the bytes as the client read them, in chunks that may
	// hold several lines or end in the middle of one, so it assembles the
	// lines itself, with a bufio.Reader for instance. A line the user entered
	// on a terminal ends in '\n'; the last line of a pipe may not, so the
	// bytes before io.EOF are a line too.
	ConsoleLine ConsoleMode = iota
	// ConsoleRaw is a console of bytes, for bridging a terminal line such as
	// a serial port. When the user's input is a terminal, the client switches
	// it to raw mode: every byte reaches Stdin at once and unchanged, Enter as
	// CR, Ctrl-C as 0x03 and Escape as 0x1b, with no local echo, and the bytes
	// the module writes to Stdout reach the terminal as they are, so the far
	// end does the echo and the line discipline.
	//
	// The module cannot count on raw mode, though: when the user's input is
	// not a terminal, or the client's output format is not text, the client
	// serves the console like a line console, and the input arrives as it was
	// read, in chunks; a user who pipes input then supplies the CR the far
	// end expects. The module forwards the bytes either way and must not
	// depend on how they are chunked.
	ConsoleRaw
)

// ConsoleOptions configures a console. The zero value opens a line console.
type ConsoleOptions struct {
	Mode ConsoleMode
}

// Console is the module's end of an open console. Stdout and Stderr are safe
// for concurrent use; a write blocks until the client took the bytes and fails
// with io.ErrClosedPipe once the console closed or the session was torn down,
// also after Stdin ended. Stdin has one reader at a time, which may run on a
// goroutine other than Run's. Close may be called from any goroutine, also
// while a Read blocks: it ends the input, the Read returns io.EOF, and the
// console stays open for output. Close is idempotent and never fails.
type Console struct {
	Stdin  io.ReadCloser
	Stdout io.Writer
	Stderr io.Writer
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
// Register panics if r.ID is empty, if r.New is nil, or if a module with the
// same ID is already registered.
func Register(r Record) {
	if r.ID == "" {
		panic("module ID missing")
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
