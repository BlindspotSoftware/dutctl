// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flash

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recSession records everything a module prints and serves one file.
type recSession struct {
	out   strings.Builder
	files map[string][]byte
}

func (s *recSession) Print(a ...any)                 { fmt.Fprint(&s.out, a...) }
func (s *recSession) Printf(format string, a ...any) { fmt.Fprintf(&s.out, format, a...) }
func (s *recSession) Println(a ...any)               { fmt.Fprintln(&s.out, a...) }

func (s *recSession) Console() (io.Reader, io.Writer, io.Writer) {
	return nil, nil, nil
}

func (s *recSession) RequestFile(name string) (io.Reader, error) {
	b, ok := s.files[name]
	if !ok {
		return nil, fmt.Errorf("no file %q", name)
	}

	return bytes.NewReader(b), nil
}

func (s *recSession) SendFile(string, io.Reader) error { return nil }

// fakeLab provides a fake flash tool, a fake uhubctl and a fake sysfs. Both
// tools append their arguments to calls.log. A control file makes the next
// matching flash tool call fail once: fail-w (a write), fail-v (a verify),
// fail-probe (--flash-name).
type fakeLab struct {
	dir   string
	tool  string
	sysfs string
}

// fast shortens the waits of r and points it at the fake sysfs.
func (l *fakeLab) fast(r *Recover) {
	r.settle, r.enumerate, r.interval, r.sysfs = time.Millisecond, time.Second, time.Millisecond, l.sysfs
}

const fakeToolScript = `#!/bin/sh
echo "flashprog $*" >> "%[1]s/calls.log"
for a in "$@"; do
  case "$a" in
    -w) f=fail-w ;;
    -v) f=fail-v ;;
    --flash-name) f=fail-probe ;;
  esac
done
if [ -n "$f" ] && [ -f "%[1]s/$f" ]; then
  rm -f "%[1]s/$f"
  echo "SPI bulk read failed! (fake)"
  exit 1
fi
echo "fake flashprog: ok"
`

const fakeUhubctlScript = `#!/bin/sh
echo "uhubctl $*" >> "%[1]s/calls.log"
`

func newFakeLab(t *testing.T) *fakeLab {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")

	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := filepath.Join(bin, "flashprog")
	writeExec(t, tool, fmt.Sprintf(fakeToolScript, dir))
	writeExec(t, filepath.Join(bin, "uhubctl"), fmt.Sprintf(fakeUhubctlScript, dir))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A fake sysfs with the programmer attached.
	usb := filepath.Join(dir, "sys", "1-1.3")
	if err := os.MkdirAll(usb, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(usb, "idVendor"), "0483\n")
	writeFile(t, filepath.Join(usb, "idProduct"), "dada\n")

	// The module works relative to the dutagent working directory.
	t.Chdir(t.TempDir())

	return &fakeLab{dir: dir, tool: tool, sysfs: filepath.Join(dir, "sys")}
}

func (l *fakeLab) failNext(t *testing.T, what string) {
	t.Helper()
	writeFile(t, filepath.Join(l.dir, what), "")
}

// calls returns the logged calls since the last call to calls.
func (l *fakeLab) calls(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(l.dir, "calls.log")

	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	_ = os.Remove(path)

	if len(b) == 0 {
		return nil
	}

	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// testRegions is a small layout of a 64 KiB image: boot and data are never
// written at runtime, env and rw are.
func testRegions() []Region {
	return []Region{
		{Name: "boot", Start: "0x0", End: "0x3fff"},
		{Name: "env", Start: "0x4000", End: "0x4fff"},
		{Name: "data", Start: "0x5000", End: "0xbfff"},
		{Name: "rw", Start: "0xc000", End: "0xffff"},
	}
}

func image(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, 0x10000)
}

func newFlash(t *testing.T, l *fakeLab, recover bool) *Flash {
	t.Helper()

	f := &Flash{
		Tool:           l.tool,
		Programmer:     "dediprog:spispeed=24M",
		supportedTools: []string{flashromTool, flashprogTool, dpcmdTool},
		SkipUnchanged:  &SkipUnchanged{Regions: testRegions(), Always: []string{"env", "rw"}},
	}

	if recover {
		f.Recover = &Recover{Hubs: []string{"1-1", "2"}, Device: "0483:dada", Off: "20ms", Timeout: "1s"}
	}

	if err := f.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if f.Recover != nil {
		l.fast(f.Recover)
	}

	return f
}

func write(t *testing.T, f *Flash, img []byte) (string, error) {
	t.Helper()

	s := &recSession{files: map[string][]byte{"/tmp/bmc.mtd": img}}
	err := f.Run(context.Background(), s, "write", "/tmp/bmc.mtd")

	return s.out.String(), err
}

func contains(calls []string, want string) bool {
	for _, c := range calls {
		if strings.Contains(c, want) {
			return true
		}
	}

	return false
}

func TestSkipUnchangedDecisions(t *testing.T) {
	l := newFakeLab(t)
	f := newFlash(t, l, false)
	full := "-p dediprog:spispeed=24M -w ./image"

	// 1. Nothing recorded: full write, then the hash is stored.
	out, err := write(t, f, image(1))
	if err != nil {
		t.Fatalf("write 1: %v\n%s", err, out)
	}

	calls := l.calls(t)
	if !contains(calls, full) || !strings.Contains(out, "no verified full write is recorded") {
		t.Fatalf("write 1 was not a full write:\n%v\n%s", calls, out)
	}

	if _, err := os.Stat(f.SkipUnchanged.State); err != nil {
		t.Fatalf("hash not stored: %v", err)
	}

	// 2. Same image: head check of the skipped regions, then env and rw only.
	out, err = write(t, f, image(1))
	if err != nil {
		t.Fatalf("write 2: %v\n%s", err, out)
	}

	calls = l.calls(t)
	if !contains(calls, "-l ./layout-heads -i boot-head -i data-head -N -v ./image") ||
		!contains(calls, "-l ./layout -i env -i rw -N -w ./image") || contains(calls, full) {
		t.Fatalf("write 2 did not skip:\n%v\n%s", calls, out)
	}

	heads, _ := os.ReadFile(headsPath)
	if string(heads) != "00000000:00000fff boot-head\n00005000:00005fff data-head\n" {
		t.Errorf("unexpected head layout:\n%s", heads)
	}

	// 3. Another image: full write.
	out, err = write(t, f, image(2))
	if err != nil {
		t.Fatalf("write 3: %v\n%s", err, out)
	}

	if calls = l.calls(t); !contains(calls, full) || !strings.Contains(out, "used a different image") {
		t.Fatalf("write 3 was not a full write:\n%v\n%s", calls, out)
	}

	// 4. Same image, but the chip start differs (a foreign write): full write.
	l.failNext(t, "fail-v")

	out, err = write(t, f, image(2))
	if err != nil {
		t.Fatalf("write 4: %v\n%s", err, out)
	}

	if calls = l.calls(t); !contains(calls, full) || !strings.Contains(out, "differs from the image") {
		t.Fatalf("write 4 was not a full write:\n%v\n%s", calls, out)
	}
}

func TestFailedFullWriteForgetsHash(t *testing.T) {
	l := newFakeLab(t)
	f := newFlash(t, l, false)

	if _, err := write(t, f, image(1)); err != nil {
		t.Fatal(err)
	}

	l.failNext(t, "fail-w")

	if out, err := write(t, f, image(2)); err == nil {
		t.Fatalf("a failed write reported success:\n%s", out)
	}

	if _, err := os.Stat(f.SkipUnchanged.State); !os.IsNotExist(err) {
		t.Fatalf("a failed full write kept the stored hash (err=%v)", err)
	}
}

func TestFailedShortWriteKeepsHash(t *testing.T) {
	l := newFakeLab(t)
	f := newFlash(t, l, false)

	if _, err := write(t, f, image(1)); err != nil {
		t.Fatal(err)
	}

	l.failNext(t, "fail-w")

	if out, err := write(t, f, image(1)); err == nil {
		t.Fatalf("a failed write reported success:\n%s", out)
	}

	// Only the always regions were touched, so the skipped ones still match.
	if storedHash(f.SkipUnchanged.State) == "" {
		t.Fatal("a failed short write dropped the stored hash")
	}
}

func TestRecoverAfterFailedWrite(t *testing.T) {
	l := newFakeLab(t)
	f := newFlash(t, l, true)

	l.failNext(t, "fail-w")

	out, err := write(t, f, image(1))
	if err != nil {
		t.Fatalf("write with recovery failed: %v\n%s", err, out)
	}

	calls := l.calls(t)
	for _, want := range []string{"uhubctl -l 1-1 -a off", "uhubctl -l 2 -a off", "uhubctl -l 1-1 -a on", "uhubctl -l 2 -a on"} {
		if !contains(calls, want) {
			t.Errorf("missing %q in %v", want, calls)
		}
	}

	if !strings.Contains(out, "Recovering the programmer") || !strings.Contains(out, "The programmer answers again") {
		t.Errorf("recovery not reported:\n%s", out)
	}

	if storedHash(f.SkipUnchanged.State) == "" {
		t.Error("the full write after recovery did not store the hash")
	}
}

func TestRecoverBeforeWriteWhenProbeFails(t *testing.T) {
	l := newFakeLab(t)
	f := newFlash(t, l, true)

	l.failNext(t, "fail-probe")

	out, err := write(t, f, image(1))
	if err != nil {
		t.Fatalf("write: %v\n%s", err, out)
	}

	if !strings.Contains(out, "does not answer, recovering it first") {
		t.Errorf("no recovery before the write:\n%s", out)
	}

	if calls := l.calls(t); !contains(calls, "uhubctl -l 1-1 -a off") {
		t.Errorf("no power cut: %v", calls)
	}
}

func TestPowerCycleAlwaysPowersOn(t *testing.T) {
	l := newFakeLab(t)
	r := &Recover{Hubs: []string{"1-1", "2"}, Device: "0483:dada", Off: "5s"}

	if err := r.validate(); err != nil {
		t.Fatal(err)
	}

	l.fast(r)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := r.powerCycle(ctx, &recSession{}, func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("a cancelled power cycle reported success")
	}

	calls := l.calls(t)
	if !contains(calls, "uhubctl -l 1-1 -a on") || !contains(calls, "uhubctl -l 2 -a on") {
		t.Fatalf("ports left off after cancel: %v", calls)
	}
}

func TestOpenBMCPreset(t *testing.T) {
	s := &SkipUnchanged{Preset: presetOpenBMCStatic}

	if err := s.validate(); err != nil {
		t.Fatal(err)
	}

	if strings.Join(s.Always, ",") != "u-boot-env,rwfs" {
		t.Errorf("default always = %v", s.Always)
	}

	for size, rwfs := range map[int64]int64{64 << 20: 0x2a00000, 128 << 20: 0x6000000} {
		layout, err := s.layout(size)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}

		if last := layout[len(layout)-1]; last.name != "rwfs" || last.start != rwfs || last.end != size-1 {
			t.Errorf("size %d: rwfs = %+v", size, last)
		}
	}

	if _, err := s.layout(32 << 20); err == nil {
		t.Error("32 MiB image accepted by a preset that has no such layout")
	}
}

func TestInitRejectsBadConfig(t *testing.T) {
	l := newFakeLab(t)

	tests := map[string]*Flash{
		"dpcmd with skip": {Tool: "/bin/sh", SkipUnchanged: &SkipUnchanged{Preset: presetOpenBMCStatic}},
		"preset and regions": {Programmer: "p", SkipUnchanged: &SkipUnchanged{
			Preset: presetOpenBMCStatic, Regions: testRegions()}},
		"unknown preset": {Programmer: "p", SkipUnchanged: &SkipUnchanged{Preset: "coreboot"}},
		"overlap": {Programmer: "p", SkipUnchanged: &SkipUnchanged{Always: []string{"b"}, Regions: []Region{
			{Name: "a", Start: "0", End: "0x1000"}, {Name: "b", Start: "0x1000", End: "0x2000"}}}},
		"always not in layout": {Programmer: "p", SkipUnchanged: &SkipUnchanged{
			Regions: testRegions(), Always: []string{"nvram"}}},
		"always covers all": {Programmer: "p", SkipUnchanged: &SkipUnchanged{
			Regions: testRegions(), Always: []string{"boot", "env", "data", "rw"}}},
		"recover without hubs": {Programmer: "p", Recover: &Recover{Device: "0483:dada"}},
		"recover bad device":   {Programmer: "p", Recover: &Recover{Hubs: []string{"1-1"}, Device: "dediprog"}},
		"recover bad off":      {Programmer: "p", Recover: &Recover{Hubs: []string{"1-1"}, Device: "0483:dada", Off: "15"}},
	}

	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			f.supportedTools = []string{flashromTool, flashprogTool, dpcmdTool}
			if f.Tool == "" {
				f.Tool = l.tool
			} else {
				// Present the shell as dpcmd.
				dpcmd := filepath.Join(l.dir, "bin", dpcmdTool)
				writeExec(t, dpcmd, "#!/bin/sh\n")
				f.Tool = dpcmd
			}

			if err := f.Init(context.Background()); err == nil {
				t.Error("Init accepted a bad configuration")
			}
		})
	}
}
