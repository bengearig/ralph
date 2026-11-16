package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
	"unicode/utf8"
)

const (
	maxOutput  = 8000
	cutMarker  = "[output cut]\n"
	waitDelay  = 2 * time.Second
	reapWindow = 5 * time.Second
)

// Runner runs verification commands in Dir.
type Runner struct {
	Dir     string
	Timeout time.Duration
}

// Run runs the commands one after another and stops at the first failure.
// A cancelled context kills the running command and returns the report so
// far with an error wrapping the context's error.
func (r Runner) Run(ctx context.Context, commands []string) (Report, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	var report Report
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("verification cancelled: %w", err)
		}
		res, err := r.runOne(ctx, command, timeout)
		if err != nil {
			return report, fmt.Errorf("verification cancelled: %w", err)
		}
		report.Results = append(report.Results, res)
		if !res.Passed() {
			break
		}
	}
	return report, nil
}

func (r Runner) runOne(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	res := Result{Command: command}
	out := &tailWriter{}
	cmd := newCommand(command)
	cmd.Dir = r.Dir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = waitDelay

	start := time.Now()
	if err := cmd.Start(); err != nil {
		res.ExitCode = -1
		res.StartErr = err.Error()
		res.Duration = time.Since(start)
		return res, nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var ctxErr error
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		res.TimedOut = true
		killTree(cmd)
		waitErr = <-done
	case <-ctx.Done():
		ctxErr = ctx.Err()
		killTree(cmd)
		waitErr = <-done
	}
	killTree(cmd)
	waitGone(cmd)
	res.Duration = time.Since(start)
	res.Output = tail(out.Bytes())

	if ctxErr != nil {
		return res, ctxErr
	}
	switch {
	case res.TimedOut:
		res.ExitCode = -1
	case waitErr == nil || errors.Is(waitErr, exec.ErrWaitDelay):
		res.ExitCode = cmd.ProcessState.ExitCode()
	default:
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			res.ExitCode = exit.ExitCode()
		} else {
			res.ExitCode = -1
			res.StartErr = waitErr.Error()
		}
	}
	return res, nil
}

// tailWriter keeps the last maxOutput+1 bytes written to it, enough to tell
// whether more than maxOutput bytes were written.
type tailWriter struct {
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	if len(w.buf) > 2*(maxOutput+1) {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-maxOutput-1:]...)
	}
	return n, nil
}

func (w *tailWriter) Bytes() []byte {
	if len(w.buf) > maxOutput+1 {
		return w.buf[len(w.buf)-maxOutput-1:]
	}
	return w.buf
}

// tail returns the last maxOutput bytes of b, cut at a line boundary and at
// valid UTF-8 and marked when anything was dropped. b holds at most the last
// maxOutput+1 bytes of the output, so a longer b means the output was cut.
func tail(b []byte) string {
	if len(b) <= maxOutput {
		return string(b)
	}
	window := b[len(b)-maxOutput:]
	if i := bytes.IndexByte(window, '\n'); i >= 0 {
		window = window[i+1:]
	}
	for len(window) > 0 {
		if r, size := utf8.DecodeRune(window); r != utf8.RuneError || size > 1 {
			break
		}
		window = window[1:]
	}
	return cutMarker + string(window)
}
