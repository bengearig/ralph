package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func emptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestDetectFoundWithVersion(t *testing.T) {
	path := fakeClaude(t, `echo "2.1.325 (Claude Code)"`)
	d := New().Detect(context.Background(), t.TempDir())
	if !d.Found || d.Version != "2.1.325" || !strings.Contains(d.Detail, path) {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectVersionWithSuffix(t *testing.T) {
	fakeClaude(t, `echo "3.0.0-beta.1 (Claude Code)"`)
	d := New().Detect(context.Background(), t.TempDir())
	if !d.Found || d.Version != "3.0.0-beta.1" {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectVersionOnlyStdout(t *testing.T) {
	fakeClaude(t, `echo "9.9.9 noise" >&2; echo "2.1.325 (Claude Code)"`)
	d := New().Detect(context.Background(), t.TempDir())
	if d.Version != "2.1.325" {
		t.Fatalf("got %+v", d)
	}
	fakeClaude(t, `echo "9.9.9 noise" >&2; echo "hello"`)
	if d := New().Detect(context.Background(), t.TempDir()); d.Version != "" {
		t.Fatalf("stderr leaked: %+v", d)
	}
}

func TestDetectUnparsable(t *testing.T) {
	fakeClaude(t, `echo "hello there"`)
	d := New().Detect(context.Background(), t.TempDir())
	if !d.Found || d.Version != "" || !strings.Contains(d.Detail, "unrecognized output") {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectExitFailure(t *testing.T) {
	fakeClaude(t, `exit 1`)
	d := New().Detect(context.Background(), t.TempDir())
	if !d.Found || d.Version != "" || !strings.Contains(d.Detail, "exited with status 1") {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeClaude(t, `echo $$ > `+pidFile+`; exec sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	d := New().Detect(ctx, t.TempDir())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %v", elapsed)
	}
	if !d.Found || d.Version != "" || !strings.Contains(d.Detail, "timed out") {
		t.Fatalf("got %+v", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still running", pid)
}

func TestDetectRawOutputTruncated(t *testing.T) {
	fakeClaude(t, `printf 'x%.0s' $(seq 1 500); echo`)
	d := New().Detect(context.Background(), t.TempDir())
	if !strings.Contains(d.Detail, strings.Repeat("x", 80)) || strings.Contains(d.Detail, strings.Repeat("x", 81)) {
		t.Fatalf("got %q", d.Detail)
	}
}

func TestDetectNoCommandButDirectory(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := New().Detect(context.Background(), root)
	if !d.Found || d.Version != "" || !strings.Contains(d.Detail, ".claude directory") {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectNothing(t *testing.T) {
	emptyPath(t)
	d := New().Detect(context.Background(), t.TempDir())
	if d.Found || d.Version != "" || d.Detail == "" {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectDoesNotReadStdin(t *testing.T) {
	fakeClaude(t, `if read -r line; then echo "line: $line"; else echo "2.1.325"; fi`)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.WriteString("input\n"); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	d := New().Detect(context.Background(), t.TempDir())
	if d.Version != "2.1.325" {
		t.Fatalf("stdin inherited: %+v", d)
	}
}

func TestDetectWritesNothing(t *testing.T) {
	fakeClaude(t, `echo "2.1.325 (Claude Code)"`)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	list := func() []string {
		var names []string
		_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
			names = append(names, p)
			return nil
		})
		return names
	}
	before := list()
	New().Detect(context.Background(), root)
	if after := list(); !reflect.DeepEqual(before, after) {
		t.Fatalf("listing changed: %v -> %v", before, after)
	}
}
