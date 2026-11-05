// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
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
