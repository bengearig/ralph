//go:build !windows

package headless

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness"
)

type fakeParser struct {
	mu    sync.Mutex
	lines []string
	out   harness.Outcome
}

func (p *fakeParser) Line(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = append(p.lines, string(b))
}
func (p *fakeParser) Usage() harness.Usage { return harness.Usage{} }
func (p *fakeParser) Outcome() harness.Outcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.out
	out.Text = strings.Join(p.lines, "|")
	return out
}

func script(t *testing.T, body string) harness.Invocation {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return harness.Invocation{Command: path, Dir: dir}
}

func mustRun(t *testing.T, inv harness.Invocation, opt Options) (Result, *fakeParser) {
	t.Helper()
	p := &fakeParser{}
	res, err := Run(context.Background(), inv, p, opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, p
}

func equalLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestRunFeedsLinesToParser(t *testing.T) {
	_, p := mustRun(t, script(t, "echo one; echo two; echo three"), Options{})
	equalLines(t, p.lines, "one", "two", "three")
}

func TestRunFinalLineWithoutNewline(t *testing.T) {
	_, p := mustRun(t, script(t, "echo one; printf last"), Options{})
	equalLines(t, p.lines, "one", "last")
}

func TestRunSkipsEmptyLinesAndTrimsCarriageReturn(t *testing.T) {
	_, p := mustRun(t, script(t, `printf 'a\r\n\n\r\nb\n'`), Options{})
	equalLines(t, p.lines, "a", "b")
}

func TestRunWritesStdoutUnchanged(t *testing.T) {
	var buf bytes.Buffer
	mustRun(t, script(t, `printf 'a\r\n\n\nb'`), Options{Stdout: &buf})
	if got := buf.String(); got != "a\r\n\n\nb" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunStdinPrompt(t *testing.T) {
	inv := script(t, "wc -c")
	inv.Stdin = strings.Repeat("x", 1<<20)
	res, p := mustRun(t, inv, Options{Timeout: 20 * time.Second})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d, stderr %q", res.ExitCode, res.Stderr)
	}
	equalLines(t, p.lines, strconv.Itoa(1<<20))
}

func TestRunEmptyStdinIsClosed(t *testing.T) {
	res, _ := mustRun(t, script(t, "cat"), Options{Timeout: 10 * time.Second})
	if res.TimedOut || res.Duration > 5*time.Second {
		t.Errorf("cat did not return at once: %+v", res)
	}
}

func TestRunEnvAddsAndOverrides(t *testing.T) {
	t.Setenv("RALPH_T_EXISTING", "old")
	t.Setenv("RALPH_T_KEPT", "kept")
	inv := script(t, `echo "$RALPH_T_EXISTING $RALPH_T_NEW $RALPH_T_KEPT"`)
	inv.Env = []string{"RALPH_T_EXISTING=new", "RALPH_T_NEW=added"}
	_, p := mustRun(t, inv, Options{})
	equalLines(t, p.lines, "new added kept")
}

func TestRunDir(t *testing.T) {
	inv := script(t, "pwd -P")
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	inv.Dir = dir
	_, p := mustRun(t, inv, Options{})
	equalLines(t, p.lines, want)
}

func TestRunPWDIsTheDirectory(t *testing.T) {
	t.Setenv("PWD", t.TempDir())
	dir := t.TempDir()
	_, p := mustRun(t, harness.Invocation{Command: "env", Dir: dir}, Options{})
	if !slices.Contains(p.lines, "PWD="+dir) {
		t.Errorf("environment lacks PWD=%s: %q", dir, p.lines)
	}
}

func TestRunMissingCommand(t *testing.T) {
	_, err := Run(context.Background(), harness.Invocation{Command: "ralph-no-such-command-xyz", Dir: t.TempDir()}, &fakeParser{}, Options{})
	want := `headless: command "ralph-no-such-command-xyz" was not found on the path`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestRunMissingDir(t *testing.T) {
	inv := script(t, "true")
	missing := filepath.Join(t.TempDir(), "nope")
	inv.Dir = missing
	_, err := Run(context.Background(), inv, &fakeParser{}, Options{})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("err = %v, want one naming %q", err, missing)
	}
}

func TestRunExitCode(t *testing.T) {
	res, _ := mustRun(t, script(t, "exit 7"), Options{})
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
}

func TestRunSignalExitCode(t *testing.T) {
	res, _ := mustRun(t, script(t, "kill -9 $$"), Options{})
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
}

func TestRunStderrKeepsLastBytes(t *testing.T) {
	inv := script(t, `i=0; while [ $i -lt 40 ]; do head -c 1000 /dev/zero | tr '\0' a >&2; i=$((i+1)); done; printf END >&2`)
	res, _ := mustRun(t, inv, Options{})
	if len(res.Stderr) != stderrKept {
		t.Errorf("len(Stderr) = %d, want %d", len(res.Stderr), stderrKept)
	}
	if !strings.HasSuffix(res.Stderr, "aaaEND") {
		t.Errorf("end of stderr not kept: %q", res.Stderr[len(res.Stderr)-10:])
	}
}

func TestRunLongLine(t *testing.T) {
	inv := script(t, `head -c 1048576 /dev/zero | tr '\0' x; echo; echo after`)
	_, p := mustRun(t, inv, Options{})
	if len(p.lines) != 2 || len(p.lines[0]) != 1<<20 || p.lines[1] != "after" {
		t.Errorf("got %d lines", len(p.lines))
	}
}

func TestRunOversizeLineSkipped(t *testing.T) {
	old := maxLine
	maxLine = 100
	t.Cleanup(func() { maxLine = old })
	var buf bytes.Buffer
	inv := script(t, `echo ok; head -c 300000 /dev/zero | tr '\0' x; echo; echo after`)
	res, p := mustRun(t, inv, Options{Stdout: &buf})
	equalLines(t, p.lines, "ok", "after")
	if res.SkippedLines != 1 {
		t.Errorf("SkippedLines = %d", res.SkippedLines)
	}
	if want := "ok\n" + strings.Repeat("x", 300000) + "\nafter\n"; buf.String() != want {
		t.Errorf("writer got %d bytes, want %d", buf.Len(), len(want))
	}
}

func gone(pid int) bool {
	for range 100 {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if stat, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); e == nil && bytes.Contains(stat, []byte(") Z")) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	inv := script(t, `trap '' TERM; sleep 60 & echo $! > "$(dirname "$0")/child.pid"; echo $$ > "$(dirname "$0")/self.pid"; while :; do sleep 1; done`)
	res, _ := mustRun(t, inv, Options{Timeout: 300 * time.Millisecond, Grace: 300 * time.Millisecond})
	if !res.TimedOut || res.Canceled {
		t.Errorf("TimedOut=%v Canceled=%v", res.TimedOut, res.Canceled)
	}
	if res.Duration > 3*time.Second {
		t.Errorf("Duration = %v", res.Duration)
	}
	for _, name := range []string{"child.pid", "self.pid"} {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(inv.Command), name))
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if !gone(pid) {
			t.Errorf("%s: process %d still running", name, pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func TestRunTerminatesGracefullyFirst(t *testing.T) {
	inv := script(t, `trap 'echo term > "$(dirname "$0")/marker"; exit 0' TERM; sleep 60 & wait`)
	res, _ := mustRun(t, inv, Options{Timeout: 300 * time.Millisecond, Grace: 5 * time.Second})
	if _, err := os.Stat(filepath.Join(filepath.Dir(inv.Command), "marker")); err != nil {
		t.Errorf("marker missing: %v", err)
	}
	if !res.TimedOut || res.Duration > 3*time.Second {
		t.Errorf("TimedOut=%v Duration=%v", res.TimedOut, res.Duration)
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	res, err := Run(ctx, script(t, "sleep 60"), &fakeParser{}, Options{Timeout: time.Minute, Grace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Canceled || res.TimedOut {
		t.Errorf("Canceled=%v TimedOut=%v", res.Canceled, res.TimedOut)
	}
}

func TestRunDescendantHoldingPipeReturns(t *testing.T) {
	inv := script(t, "sleep 30 &\necho hi")
	start := time.Now()
	res, p := mustRun(t, inv, Options{})
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("Run took %v", d)
	}
	equalLines(t, p.lines, "hi")
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
}

func TestRunOutcomeFromParser(t *testing.T) {
	p := &fakeParser{out: harness.Outcome{Finished: true, Success: true, SessionID: "s1"}}
	res, err := Run(context.Background(), script(t, "echo a; echo b"), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Outcome.Finished || res.Outcome.SessionID != "s1" || res.Outcome.Text != "a|b" {
		t.Errorf("Outcome = %+v", res.Outcome)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRunWriterErrorReturned(t *testing.T) {
	res, err := Run(context.Background(), script(t, "echo a; exit 2"), &fakeParser{}, Options{Stdout: failWriter{}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if res.ExitCode != 2 || res.Duration == 0 {
		t.Errorf("Result not filled in: %+v", res)
	}
}
