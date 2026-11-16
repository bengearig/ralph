//go:build !windows

package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunPassesAndRecords(t *testing.T) {
	rep, err := Runner{Dir: t.TempDir()}.Run(context.Background(), []string{"echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || !rep.Passed() {
		t.Fatalf("report = %+v", rep)
	}
	r := rep.Results[0]
	if r.Command != "echo hi" || r.ExitCode != 0 || r.Output != "hi\n" || r.Duration <= 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunEmptyListPasses(t *testing.T) {
	rep, err := Runner{Dir: t.TempDir()}.Run(context.Background(), nil)
	if err != nil || len(rep.Results) != 0 || !rep.Passed() {
		t.Fatalf("report = %+v, err = %v", rep, err)
	}
}

func TestRunStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	rep, err := Runner{Dir: dir}.Run(context.Background(), []string{"true", "false", "touch third"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 2 || rep.Passed() {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "third")); err == nil {
		t.Fatal("third command ran")
	}
}

func TestRunRecordsExitCode(t *testing.T) {
	rep, _ := Runner{Dir: t.TempDir()}.Run(context.Background(), []string{"exit 3"})
	f, ok := rep.Failed()
	if !ok || f.ExitCode != 3 || f.TimedOut || f.StartErr != "" {
		t.Fatalf("failed = %+v, %v", f, ok)
	}
}

func TestRunCombinesStdoutAndStderrInOrder(t *testing.T) {
	rep, _ := Runner{Dir: t.TempDir()}.Run(context.Background(), []string{"echo one; echo two >&2; echo three"})
	if got := rep.Results[0].Output; got != "one\ntwo\nthree\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunUsesDir(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := Runner{Dir: dir}.Run(context.Background(), []string{"pwd"})
	if got := strings.TrimSpace(rep.Results[0].Output); got != dir {
		t.Fatalf("pwd = %q, want %q", got, dir)
	}
}

func TestRunNoStdin(t *testing.T) {
	start := time.Now()
	rep, _ := Runner{Dir: t.TempDir(), Timeout: 20 * time.Second}.Run(context.Background(), []string{"cat; echo done"})
	r := rep.Results[0]
	if !r.Passed() || r.Output != "done\n" || time.Since(start) > 10*time.Second {
		t.Fatalf("result = %+v", r)
	}
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func readPid(t *testing.T, file string) int {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if !alive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("process %d still running", pid)
}

const spawnSleeper = "sleep 30 & echo $! > pid; wait"

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	rep, err := Runner{Dir: dir, Timeout: 300 * time.Millisecond}.Run(context.Background(), []string{spawnSleeper})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	f, ok := rep.Failed()
	if !ok || !f.TimedOut || f.ExitCode != -1 {
		t.Fatalf("failed = %+v, %v", f, ok)
	}
	waitDead(t, readPid(t, filepath.Join(dir, "pid")))
}

func TestRunStartFailure(t *testing.T) {
	rep, err := Runner{Dir: filepath.Join(t.TempDir(), "missing")}.Run(context.Background(), []string{"true", "true"})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := rep.Failed()
	if !ok || f.StartErr == "" || f.ExitCode != -1 || len(rep.Results) != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRunContextCancelled(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(filepath.Join(dir, "pid")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	rep, err := Runner{Dir: dir}.Run(ctx, []string{"true", spawnSleeper, "touch after"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(rep.Results) != 1 || !rep.Passed() {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "after")); err == nil {
		t.Fatal("command ran after cancel")
	}
	waitDead(t, readPid(t, filepath.Join(dir, "pid")))
}
