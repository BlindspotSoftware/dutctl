// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package board simulates the board that the demo modules drive: a power
// line, a flash that holds its firmware, with a write-protect line, and a
// serial line to a tiny shell.
// It stands for the hardware and its drivers, so a reader of the demo modules
// need not read it, as the reader of a real module need not read the serial
// port's driver.
package board

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	// FlashSize is the size of the board's flash; a bigger image does not fit.
	FlashSize = 1 << 20
	// Prompt is the shell's prompt.
	Prompt = "demo# "

	// uartBacklog bounds what the board keeps of its serial output while no
	// console shows it; older bytes are lost, as in a console server's buffer.
	uartBacklog = 4096
	// maxLine bounds the shell's input line; further keys are ignored.
	maxLine = 256
	// digestLen is how many hex digits of an image's SHA-256 the board shows.
	digestLen = 12
)

// Control characters the shell's line editing knows.
const (
	ctrlC     = 0x03
	backspace = 0x08
	del       = 0x7f
)

// Errors of a write to the flash.
var (
	// ErrPowered is returned when the flash is written while the board is on.
	ErrPowered = errors.New("the board is on: power it off before writing its flash")
	// ErrWriteProtected is returned when the flash is written while its
	// write-protect line is on.
	ErrWriteProtected = errors.New("the flash is write-protected: switch its write-protect line off before writing it")
)

// Line is an input line of the board, which is on or off.
type Line string

// The board's lines.
const (
	// Power powers the board: switched on, it boots.
	Power Line = "power"
	// WriteProtect protects the flash from writes while it is on.
	WriteProtect Line = "write-protect"
)

// Image is a firmware image in the board's flash.
type Image struct {
	// Name is the base name of the file it was written from.
	Name string
	// Data is its content.
	Data []byte
}

// Factory returns the image the board leaves the factory with.
func Factory() Image {
	return Image{Name: "factory.bin", Data: []byte("demo board firmware 1.0\n")}
}

// Summary describes img in one line: name, size and the start of its SHA-256.
func (img Image) Summary() string {
	sum := sha256.Sum256(img.Data)

	return fmt.Sprintf("%s, %d bytes, sha256 %s", img.Name, len(img.Data), hex.EncodeToString(sum[:])[:digestLen])
}

// Board is the simulated device under test. A user's commands run side by
// side, so every access holds mu.
type Board struct {
	// mu guards the fields below.
	mu sync.Mutex
	// lines holds the lines that are on.
	lines map[Line]bool
	// firmware is the image in the flash, which the board boots.
	firmware Image
	// uart is what the board sent on its serial line and no console has shown
	// yet, at most uartBacklog bytes.
	uart []byte
	// changed is closed and renewed whenever the board sends on its serial
	// line, so a console can wait for that.
	changed chan struct{}
	// line is the shell's input line typed so far.
	line []byte
	// afterCR tells whether the last key was a CR, so that the LF of a CR LF
	// does not end a second, empty line.
	afterCR bool
}

// New returns a board that is off and holds the factory image.
func New() *Board {
	return &Board{lines: map[Line]bool{}, firmware: Factory(), changed: make(chan struct{})}
}

// On tells whether line is on.
func (b *Board) On(line Line) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.lines[line]
}

// Switch switches line on or off and tells whether that changed it. Switching
// Power on boots the board: it sends its boot messages and the shell's prompt
// on its serial line.
func (b *Board) Switch(line Line, on bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.lines[line] == on {
		return false
	}

	b.lines[line] = on

	if line != Power {
		return true
	}

	b.line, b.afterCR = nil, false

	if on {
		b.send("\r\nDemo board boot ROM 1.0\r\nBooting " + b.firmware.Summary() +
			"\r\nWelcome to the demo board. Type help for its commands.\r\n" + Prompt)
	} else {
		b.send("\r\n[the board lost power]\r\n")
	}

	return true
}

// Writable tells why the flash cannot be written now, with ErrPowered or
// ErrWriteProtected, or returns nil.
func (b *Board) Writable() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.writable()
}

// writable is Writable. b.mu must be held.
func (b *Board) writable() error {
	switch {
	case b.lines[Power]:
		return ErrPowered
	case b.lines[WriteProtect]:
		return ErrWriteProtected
	default:
		return nil
	}
}

// Flash writes img to the flash, unless Writable returns an error.
func (b *Board) Flash(img Image) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	err := b.writable()
	if err != nil {
		return err
	}

	b.firmware = img

	return nil
}

// Firmware returns the image in the flash.
func (b *Board) Firmware() Image {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.firmware
}

// TakeOutput returns what the board sent since it was last called, and a
// channel that is closed when the board sends something new.
func (b *Board) TakeOutput() ([]byte, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := b.uart
	b.uart = nil

	return out, b.changed
}

// TypeIn feeds keys to the board's serial line, where its shell does the echo
// and the line editing, as the far end of a raw console does. A board that is
// off ignores them. It tells whether the shell logged out; the keys after the
// exit command are dropped then.
func (b *Board) TypeIn(keys []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.lines[Power] {
		return false
	}

	for _, key := range keys {
		if b.press(key) {
			return true
		}
	}

	return false
}

// send puts s on the board's serial line. b.mu must be held.
func (b *Board) send(s string) {
	b.uart = append(b.uart, s...)
	if len(b.uart) > uartBacklog {
		b.uart = b.uart[len(b.uart)-uartBacklog:]
	}

	close(b.changed)
	b.changed = make(chan struct{})
}

// press handles one key and tells whether it made the shell log out. b.mu
// must be held.
func (b *Board) press(key byte) bool {
	afterCR := b.afterCR
	b.afterCR = key == '\r'

	switch {
	case key == '\r' || (key == '\n' && !afterCR):
		b.send("\r\n")

		return b.runLine()
	case key == '\n':
		// The LF of a CR LF: the CR ended the line already.
	case key == backspace || key == del:
		b.erase()
	case key == ctrlC:
		b.line = nil
		b.send("^C\r\n" + Prompt)
	case key >= ' ' && key < del:
		b.insert(key)
	}

	return false
}

// insert adds key to the line and echoes it, unless the line is full. b.mu
// must be held.
func (b *Board) insert(key byte) {
	if len(b.line) < maxLine {
		b.line = append(b.line, key)
		b.send(string(key))
	}
}

// erase removes the last key from the line, on the terminal too. b.mu must be
// held.
func (b *Board) erase() {
	if len(b.line) > 0 {
		b.line = b.line[:len(b.line)-1]
		b.send("\b \b")
	}
}

// runLine runs the shell's input line and tells whether it logged out. b.mu
// must be held.
func (b *Board) runLine() bool {
	cmd, arg, _ := strings.Cut(strings.TrimSpace(string(b.line)), " ")
	b.line = nil

	switch cmd {
	case "":
	case "help":
		b.send("Commands: help, version, echo <text>, exit\r\n")
	case "version":
		b.send(b.firmware.Summary() + "\r\n")
	case "echo":
		b.send(arg + "\r\n")
	case "exit":
		b.send("logout\r\n")

		return true
	default:
		b.send(cmd + ": command not found\r\n")
	}

	b.send(Prompt)

	return false
}
