package tickets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Runner runs a function in the background after a trigger, one run at a time. Triggers that arrive while a run
// is going cause exactly one more run.
type Runner struct {
	run     func(ctx context.Context) error
	timeout time.Duration

	mu      sync.Mutex
	running bool
	pending bool
	idle    chan struct{}
}

// NewRunner returns a Runner that gives each run a context that ends after timeout.
func NewRunner(run func(ctx context.Context) error, timeout time.Duration) *Runner {
	return &Runner{run: run, timeout: timeout}
}

// Trigger asks for a run and returns at once.
func (r *Runner) Trigger() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		r.pending = true
		return
	}
	r.running = true
	r.idle = make(chan struct{})
	go r.loop()
}

func (r *Runner) loop() {
	for {
		r.once()
		r.mu.Lock()
		if r.pending {
			r.pending = false
			r.mu.Unlock()
			continue
		}
		r.running = false
		close(r.idle)
		r.mu.Unlock()
		return
	}
}

func (r *Runner) once() {
	defer func() {
		if p := recover(); p != nil {
			slog.Warn("ticket sync failed", "panic", fmt.Sprint(p))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	if err := r.run(ctx); err != nil {
		slog.Warn("ticket sync failed", "error", err)
	}
}

// Wait blocks until no run is running or pending, or ctx ends, and reports whether the Runner is idle.
func (r *Runner) Wait(ctx context.Context) bool {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return true
	}
	idle := r.idle
	r.mu.Unlock()
	select {
	case <-idle:
		return true
	case <-ctx.Done():
		return false
	}
}

// Auto is the pass a Runner runs after ralph writes. It does nothing unless the provider is jira and automatic
// sync is on, and it counts a busy or paused pass as success.
func Auto(ctx context.Context, d Deps) error {
	if d.Settings.Provider != "jira" || !d.Settings.AutoSync {
		return nil
	}
	d.Report = nil
	poll(ctx, d)
	_, err := Sync(ctx, d, SyncOptions{Automatic: true})
	if errors.Is(err, ErrSyncBusy) || errors.Is(err, ErrPaused) {
		return nil
	}
	return err
}

// poll checks the tickets for edits made in the system when the last check is older than the poll interval. A
// failure is logged and does not stop the pass.
func poll(ctx context.Context, d Deps) {
	last, err := NewStore(d.DB).State(ctx, stateLastPoll)
	if err != nil {
		slog.Warn("ticket poll failed", "error", err)
		return
	}
	if at, perr := time.Parse(time.RFC3339, last); perr == nil && d.now().Sub(at) < d.Settings.PollInterval {
		return
	}
	if _, err := Check(ctx, d, ObserveOptions{}); err != nil {
		slog.Warn("ticket poll failed", "error", err)
	}
}
