// Package verify runs the commands a project configures to check quick work
// before it is committed. Ralph runs them itself, so a result is judged by
// honest tests and not by what an agent reports.
package verify

import (
	"time"

	"github.com/bengearig/ralph/internal/config"
)

const (
	defaultTimeout = 10 * time.Minute
	commandsPath   = "verify.commands"
	timeoutPath    = "verify.timeout"
)

// Result is the outcome of one command.
type Result struct {
	Command  string
	ExitCode int
	Duration time.Duration
	Output   string
	TimedOut bool
	StartErr string
}

// Passed reports whether the command ran and exited with status zero in time.
func (r Result) Passed() bool {
	return r.ExitCode == 0 && !r.TimedOut && r.StartErr == ""
}

// Report holds the results of the commands that ran.
type Report struct {
	Results []Result
}

// Passed is true when no result failed, including when there are none.
func (r Report) Passed() bool {
	_, failed := r.Failed()
	return !failed
}

// Failed returns the first failing result.
func (r Report) Failed() (Result, bool) {
	for _, res := range r.Results {
		if !res.Passed() {
			return res, true
		}
	}
	return Result{}, false
}

// Settings are the verification settings read from the configuration.
type Settings struct {
	Commands []string
	Timeout  time.Duration
	// Source names the layer and place verify.commands came from; empty when
	// no commands are set.
	Source string
}

// FromConfig reads the verification settings. File sources inside root are
// shown relative to it.
func FromConfig(cfg *config.Config, root string) Settings {
	var s Settings
	if v, ok := cfg.Get(timeoutPath); ok {
		if d, err := time.ParseDuration(v.Value); err == nil {
			s.Timeout = d
		}
	}
	commands, ok := cfg.List(commandsPath)
	if !ok || len(commands) == 0 {
		return s
	}
	s.Commands = commands
	if v, ok := cfg.Get(commandsPath); ok {
		s.Source = v.Layer.String() + " (" + v.SourceText(root) + ")"
	}
	return s
}
