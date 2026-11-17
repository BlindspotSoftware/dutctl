// Copyright 2025 Blindspot Software
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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// fakeFlashprogScript stands in for flashprog, called as "-p <programmer> -r|-w <image>".
// It records the image path in <dir>/<programmer>.path. A write copies the image to
// <dir>/<programmer>.flashed, a read writes "chip of <programmer>" into it. The
// programmer "fail" makes it fail, "hang" makes it run until it is stopped. While
// <dir>/barrier exists, it waits until the tools of the programmers "a" and "b" both
// run, so their runs overlap.
const fakeFlashprogScript = `#!/bin/sh
dir='%s'
programmer=$2 operation=$3 image=$4
echo "$image" > "$dir/$programmer.path"
touch "$dir/$programmer.started"
case $programmer in
fail) echo "fake failure"; exit 1 ;;
hang) exec sleep 60 ;;
esac
if [ -e "$dir/barrier" ]; then
	tries=0
	until [ -e "$dir/a.started" ] && [ -e "$dir/b.started" ]; do
		tries=$((tries + 1))
		[ $tries -gt 1000 ] && exit 2
		sleep 0.01
	done
fi
case $operation in
-w) cp "$image" "$dir/$programmer.flashed" ;;
-r) printf 'chip of %%s' "$programmer" > "$image" ;;
esac
`

// setupFakeFlashprog writes the fake flashprog into dir and points TMPDIR at the empty
// directory tmp, where the runs create their image directories.
func setupFakeFlashprog(t *testing.T) (tool, dir, tmp string) {
	t.Helper()

	dir = t.TempDir()
	tool = filepath.Join(dir, flashprogTool)

	err := os.WriteFile(tool, fmt.Appendf(nil, fakeFlashprogScript, dir), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	tmp = t.TempDir()
	t.Setenv("TMPDIR", tmp)

	return tool, dir, tmp
}

// readFile returns the content of path, failing the test if it cannot be read.
func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(content)
}

// assertRemoved fails the test if a run left anything in tmp.
func assertRemoved(t *testing.T, tmp string) {
	t.Helper()

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) > 0 {
		t.Errorf("run left %v in the temporary directory", entries)
	}
}

func TestRunWritesConcurrently(t *testing.T) {
	tool, dir, tmp := setupFakeFlashprog(t)

	err := os.WriteFile(filepath.Join(dir, "barrier"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	programmers := []string{"a", "b"}
	errs := make([]error, len(programmers))

	var wg sync.WaitGroup

	for i, programmer := range programmers {
		wg.Go(func() {
			f := &Flash{Tool: tool, Programmer: programmer}
			sesh := &mock.Session{RequestedFileResponse: strings.NewReader("image for " + programmer)}
			errs[i] = f.Run(context.Background(), sesh, "write", programmer+".bin")
		})
	}

	wg.Wait()

	var paths []string

	for i, programmer := range programmers {
		if errs[i] != nil {
			t.Fatalf("run for %s: %v", programmer, errs[i])
		}

		if got, want := readFile(t, filepath.Join(dir, programmer+".flashed")), "image for "+programmer; got != want {
			t.Errorf("tool for %s flashed %q, want %q", programmer, got, want)
		}

		path := strings.TrimSpace(readFile(t, filepath.Join(dir, programmer+".path")))
		if filepath.Dir(filepath.Dir(path)) != tmp {
			t.Errorf("image %s of %s is not in a directory of its own under %s", path, programmer, tmp)
		}

		paths = append(paths, path)
	}

	if paths[0] == paths[1] {
		t.Errorf("runs shared the image %s", paths[0])
	}

	assertRemoved(t, tmp)
}

func TestRunReads(t *testing.T) {
	tool, _, tmp := setupFakeFlashprog(t)

	f := &Flash{Tool: tool, Programmer: "r"}
	sesh := &mock.Session{}

	err := f.Run(context.Background(), sesh, "read", "out.bin")
	if err != nil {
		t.Fatal(err)
	}

	if sesh.SentFileName != "out.bin" || string(sesh.SentFileContent) != "chip of r" {
		t.Errorf("sent %q with %q, want out.bin with %q", sesh.SentFileName, sesh.SentFileContent, "chip of r")
	}

	assertRemoved(t, tmp)
}

func TestRunRemovesImageWhenToolFails(t *testing.T) {
	tool, _, tmp := setupFakeFlashprog(t)

	f := &Flash{Tool: tool, Programmer: "fail"}
	sesh := &mock.Session{RequestedFileResponse: strings.NewReader("image")}

	err := f.Run(context.Background(), sesh, "write", "image.bin")
	if err == nil {
		t.Fatal("run succeeded with a failing tool")
	}

	assertRemoved(t, tmp)
}

func TestRunRemovesImageWhenCancelled(t *testing.T) {
	tool, dir, tmp := setupFakeFlashprog(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := &Flash{Tool: tool, Programmer: "hang"}
	sesh := &mock.Session{RequestedFileResponse: strings.NewReader("image")}
	result := make(chan error, 1)

	go func() { result <- f.Run(ctx, sesh, "write", "image.bin") }()

	// Cancel only once the tool runs, so the run must stop it before it removes the image.
	deadline := time.Now().Add(10 * time.Second)

	for {
		_, err := os.Stat(filepath.Join(dir, "hang.started"))
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("fake tool did not start")
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled run succeeded")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancelled run did not return")
	}

	assertRemoved(t, tmp)
}

func TestCmdline(t *testing.T) {
	tests := []struct {
		tool       string
		programmer string
		operation  op
		want       []string
	}{
		{flashromTool, "dediprog", opWrite, []string{"-p", "dediprog", "-w", "img"}},
		{flashprogTool, "ch341a_spi", opRead, []string{"-p", "ch341a_spi", "-r", "img"}},
		{"/usr/bin/" + dpcmdTool, "2", opWrite, []string{"--device", "2", "-u", "img"}},
		{dpcmdTool, "", opRead, []string{"-r", "img"}},
	}

	for _, tt := range tests {
		f := &Flash{Tool: tt.tool, Programmer: tt.programmer}

		got := f.cmdline(tt.operation, "img")
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s %s: got %v, want %v", tt.tool, tt.operation, got, tt.want)
		}
	}
}

// recSession records everything a module prints and serves one file.
type recSession struct {
	out   strings.Builder
	files map[string][]byte
}

func (s *recSession) Print(a ...any)                 { fmt.Fprint(&s.out, a...) }
func (s *recSession) Printf(format string, a ...any) { fmt.Fprintf(&s.out, format, a...) }
func (s *recSession) Println(a ...any)               { fmt.Fprintln(&s.out, a...) }

func (s *recSession) OpenConsole(module.ConsoleOptions) module.Console {
	return module.Console{}
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

// fakeToolScript logs the run's directory, %[2]s/flash-*, as "./", so that the
// calls read the same in every run, and keeps a copy of the head layout in %[1]s.
const fakeToolScript = `#!/bin/sh
echo "flashprog $*" | sed "s|%[2]s/flash-[^/]*/|./|g" >> "%[1]s/calls.log"
for a in "$@"; do
  case "$a" in
    -w) f=fail-w ;;
    -v) f=fail-v ;;
    --flash-name) f=fail-probe ;;
    */layout-heads) cp "$a" "%[1]s/layout-heads" ;;
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

	// Each run makes its directory under TMPDIR.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	tool := filepath.Join(bin, "flashprog")
	writeExec(t, tool, fmt.Sprintf(fakeToolScript, dir, tmp))
	writeExec(t, filepath.Join(bin, "uhubctl"), fmt.Sprintf(fakeUhubctlScript, dir))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A fake sysfs with the programmer attached.
	usb := filepath.Join(dir, "sys", "1-1.3")
	if err := os.MkdirAll(usb, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(usb, "idVendor"), "0483\n")
	writeFile(t, filepath.Join(usb, "idProduct"), "dada\n")

	// The skip state lives in the dutagent working directory.
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

	heads, _ := os.ReadFile(filepath.Join(l.dir, "layout-heads"))
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
