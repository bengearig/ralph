package tickets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/filelock"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func waitIdle(t *testing.T, r *Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !r.Wait(ctx) {
		t.Fatal("runner did not become idle")
	}
}

func TestRunnerTriggerDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	r := NewRunner(func(context.Context) error { <-release; return nil }, time.Minute)
	done := make(chan struct{})
	go func() { r.Trigger(); r.Trigger(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Trigger blocked")
	}
	close(release)
	waitIdle(t, r)
}

func TestRunnerCoalescesTriggersWhileRunning(t *testing.T) {
	var runs atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	r := NewRunner(func(context.Context) error {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}, time.Minute)
	r.Trigger()
	<-started
	for i := 0; i < 10; i++ {
		r.Trigger()
	}
	close(release)
	waitIdle(t, r)
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
}

func TestRunnerWaitReturnsWhenIdle(t *testing.T) {
	r := NewRunner(func(context.Context) error { return nil }, time.Minute)
	if !r.Wait(context.Background()) {
		t.Fatal("never-triggered runner is not idle")
	}
	r.Trigger()
	waitIdle(t, r)
}

func TestRunnerWaitHonoursContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := NewRunner(func(context.Context) error { <-release; return nil }, time.Minute)
	r.Trigger()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if r.Wait(ctx) {
		t.Fatal("Wait reported idle during a run")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Wait ignored the context")
	}
}

func TestRunnerRunTimeout(t *testing.T) {
	got := make(chan error, 1)
	r := NewRunner(func(ctx context.Context) error {
		<-ctx.Done()
		got <- ctx.Err()
		return nil
	}, 50*time.Millisecond)
	r.Trigger()
	waitIdle(t, r)
	if err := <-got; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx err = %v", err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func TestRunnerErrorIsLoggedNotReturned(t *testing.T) {
	buf := captureLog(t)
	r := NewRunner(func(context.Context) error { return errors.New("jira is down") }, time.Minute)
	r.Trigger()
	waitIdle(t, r)
	if out := buf.String(); !bytes.Contains([]byte(out), []byte("ticket sync failed")) || !bytes.Contains([]byte(out), []byte("jira is down")) {
		t.Fatalf("log = %q", out)
	}
}

func TestRunnerPanicRecovered(t *testing.T) {
	buf := captureLog(t)
	var runs atomic.Int32
	r := NewRunner(func(context.Context) error {
		if runs.Add(1) == 1 {
			panic("kaboom")
		}
		return nil
	}, time.Minute)
	r.Trigger()
	waitIdle(t, r)
	if out := buf.String(); !bytes.Contains([]byte(out), []byte("kaboom")) {
		t.Fatalf("log = %q", out)
	}
	r.Trigger()
	waitIdle(t, r)
	if runs.Load() != 2 {
		t.Fatalf("runs = %d, want the runner usable after a panic", runs.Load())
	}
}

func TestAutoNoopWhenProviderNone(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	e.deps.Settings.Provider = "none"
	e.deps.Settings.AutoSync = true
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d System calls", n)
	}
	if _, err := os.Stat(filepath.Join(e.deps.Dir, project.TicketsLockFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file: %v", err)
	}
}

func TestAutoNoopWhenAutoSyncFalse(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	e.deps.Settings.AutoSync = false
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d System calls", n)
	}
}

func TestAutoBusyIsSuccess(t *testing.T) {
	e := newEnv(t)
	e.deps.Settings.AutoSync = true
	e.unit(units.LevelBrief, "", "A")
	held, err := filelock.Acquire(filepath.Join(e.deps.Dir, project.TicketsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d System calls", n)
	}
}

func TestAutoPausedIsSuccess(t *testing.T) {
	e := newEnv(t)
	e.deps.Settings.AutoSync = true
	e.unit(units.LevelBrief, "", "A")
	if err := e.store.SetState(e.ctx, stateBlockedUntil, testNow.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d System calls", n)
	}
}

func TestAutoReturnsOtherErrors(t *testing.T) {
	e := newEnv(t)
	e.deps.Settings.AutoSync = true
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	e.unit(units.LevelBrief, "", "A")
	if err := Auto(ctx, e.deps); err == nil {
		t.Fatal("want the context error")
	}
}

func TestAutoRunsAutomaticPass(t *testing.T) {
	e := newEnv(t)
	e.deps.Settings.AutoSync = true
	e.deps.Settings.PollInterval = time.Hour
	b := e.unit(units.LevelBrief, "", "Brief")
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if e.row(b.ID).Key != "ABC-1" {
		t.Fatalf("row = %+v", e.row(b.ID))
	}
	e.sys.updateFails["ABC-1"] = fmt.Errorf("update: %w", taskmanager.ErrUnavailable)
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	for i := 0; i < 2; i++ {
		if err := Auto(e.ctx, e.deps); err != nil {
			t.Fatal(err)
		}
	}
	if row := e.row(b.ID); row.FailedStep != "fields" || row.Attempts != 1 {
		t.Fatalf("an automatic pass retried inside the poll interval: %+v", row)
	}
}

func TestAutoPrintsNothing(t *testing.T) {
	e := newEnv(t)
	e.deps.Settings.AutoSync = true
	e.unit(units.LevelBrief, "", "A")
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if len(e.log) != 0 {
		t.Fatalf("events reported: %+v", e.log)
	}
}
