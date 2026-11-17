// Package headless runs one harness process to completion: it feeds the
// prompt, reads the output a line at a time, and bounds the run in time.
package headless

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/harness"
)

const (
	defaultGrace = 5 * time.Second
	drainWindow  = 2 * time.Second
	stderrKept   = 16 << 10
	readBuffer   = 64 << 10
)

// maxLine is the longest output line passed to the parser; a variable so
// tests can lower it.
var maxLine = 32 << 20

// Options bound and observe a run.
type Options struct {
	// Timeout is the wall-clock limit; zero means none.
	Timeout time.Duration
	// Grace is how long a terminated process group has before it is killed;
	// zero means 5 seconds.
	Grace time.Duration
	// Stdout, when set, receives every byte of standard output unchanged.
	Stdout io.Writer
}

// Run starts the invocation, passes each line of its standard output to p, and
// returns when the process has exited and its output is read. A non-zero exit
// is not an error. An error is returned when the process cannot be started or
// when writing to opt.Stdout failed; the Result is filled in either way once
// the process has started.
func Run(ctx context.Context, inv harness.Invocation, p harness.Parser, opt Options) (Result, error) {
	command := inv.Command
	if !strings.ContainsAny(command, `/\`) {
		resolved, err := exec.LookPath(command)
		if err != nil {
			return Result{}, fmt.Errorf("headless: command %q was not found on the path", command)
		}
		command = resolved
	}
	if info, err := os.Stat(inv.Dir); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("headless: directory %q does not exist", inv.Dir)
	}
	grace := opt.Grace
	if grace <= 0 {
		grace = defaultGrace
	}

	cmd := exec.Command(command, inv.Args...)
	cmd.Dir = inv.Dir
	cmd.Env = mergeEnv(os.Environ(), inv.Env)
	setProcessGroup(cmd)

	var stdin io.WriteCloser
	if inv.Stdin != "" {
		var err error
		if stdin, err = cmd.StdinPipe(); err != nil {
			return Result{}, fmt.Errorf("headless: %w", err)
		}
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return Result{}, fmt.Errorf("headless: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return Result{}, fmt.Errorf("headless: %w", err)
	}
	cmd.Stdout = outW
	cmd.Stderr = errW

	begin := time.Now()
	err = cmd.Start()
	_ = outW.Close()
	_ = errW.Close()
	if err != nil {
		_ = outR.Close()
		_ = errR.Close()
		return Result{}, fmt.Errorf("headless: starting %s: %w", inv.Command, err)
	}
	defer func() {
		_ = outR.Close()
		_ = errR.Close()
	}()

	if stdin != nil {
		go func() {
			_, _ = io.WriteString(stdin, inv.Stdin)
			_ = stdin.Close()
		}()
	}

	var res Result
	var skipped int
	var writeErr error
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		skipped, writeErr = readLines(outR, opt.Stdout, p)
	}()
	tail := &tailBuffer{max: stderrKept}
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		_, _ = io.Copy(tail, errR)
	}()

	exited := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		var expire <-chan time.Time
		if opt.Timeout > 0 {
			t := time.NewTimer(opt.Timeout)
			defer t.Stop()
			expire = t.C
		}
		select {
		case <-exited:
			return
		case <-ctx.Done():
			res.Canceled = true
		case <-expire:
			res.TimedOut = true
		}
		terminateGroup(cmd)
		g := time.NewTimer(grace)
		defer g.Stop()
		select {
		case <-exited:
		case <-g.C:
			killGroup(cmd)
		}
	}()

	_ = cmd.Wait()
	close(exited)
	<-watchDone

	all := make(chan struct{})
	go func() {
		<-outDone
		<-errDone
		close(all)
	}()
	drain := time.NewTimer(drainWindow)
	select {
	case <-all:
	case <-drain.C:
		killGroup(cmd)
		_ = outR.Close()
		_ = errR.Close()
		<-all
	}
	drain.Stop()

	res.ExitCode = cmd.ProcessState.ExitCode()
	res.Duration = time.Since(begin)
	res.SkippedLines = skipped
	res.Stderr = tail.String()
	res.Outcome = p.Outcome()
	if writeErr != nil {
		return res, fmt.Errorf("headless: writing output: %w", writeErr)
	}
	return res, nil
}

func envKey(entry string) string {
	key, _, _ := strings.Cut(entry, "=")
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func mergeEnv(base, additions []string) []string {
	env := append([]string(nil), base...)
	index := make(map[string]int, len(env))
	for i, e := range env {
		index[envKey(e)] = i
	}
	for _, e := range additions {
		k := envKey(e)
		if i, ok := index[k]; ok {
			env[i] = e
			continue
		}
		index[k] = len(env)
		env = append(env, e)
	}
	return env
}

// readLines passes each non-empty line to p and copies every byte to w. A
// write error stops further writes but not the reading, so the process is
// never blocked on a full pipe.
func readLines(r io.Reader, w io.Writer, p harness.Parser) (int, error) {
	br := bufio.NewReaderSize(r, readBuffer)
	var (
		line    []byte
		over    bool
		skipped int
		werr    error
	)
	finish := func() {
		if over {
			skipped++
		} else {
			line = trimEOL(line)
			if len(line) > maxLine {
				skipped++
			} else if len(line) > 0 {
				p.Line(line)
			}
		}
		line, over = line[:0], false
	}
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if w != nil && werr == nil {
				if _, e := w.Write(chunk); e != nil {
					werr = e
				}
			}
			if !over {
				if len(line)+len(chunk) > maxLine+2 {
					over, line = true, line[:0]
				} else {
					line = append(line, chunk...)
				}
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(line) > 0 || over {
			finish()
		}
		if err != nil {
			return skipped, werr
		}
	}
}

func trimEOL(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return n, nil
	}
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.max; extra > 0 {
		t.buf = t.buf[:copy(t.buf, t.buf[extra:])]
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
