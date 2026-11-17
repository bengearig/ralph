package loop

import (
	"context"
	"io"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

// Verifier runs the verification commands; verify.Runner satisfies it.
type Verifier interface {
	Run(ctx context.Context, commands []string) (verify.Report, error)
}

// Deps are the services and settings an attempt needs.
type Deps struct {
	Units     units.Service
	Workspace workspace.Workspace
	Prompt    PromptDeps
	Config    *config.Config
	Root      string
	Adapter   harness.Adapter
	// HarnessVersion is the detected version, empty when unknown.
	HarnessVersion string
	MCP            harness.MCPServer
	// Mode is empty for what headless.NewRequest gives.
	Mode           harness.PermissionMode
	Model          string
	CommitTemplate string
	Verify         verify.Settings
	Verifier       Verifier
	// Execute is headless.Execute when nil.
	Execute func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error)
	// Recorder is a no-op when nil.
	Recorder Recorder
	// Report is not called when nil.
	Report func(Event)
}

// EventKind says what an Event reports.
type EventKind string

const (
	EventAttemptStarted EventKind = "attempt_started"
	EventAttemptFailed  EventKind = "attempt_failed"
	EventTaskDone       EventKind = "task_done"
	EventTaskStalled    EventKind = "task_stalled"
	EventWarning        EventKind = "warning"
	EventInfo           EventKind = "info"
)

// Event is one thing the loop reports as it works.
type Event struct {
	Kind    EventKind
	Task    string
	Attempt int
	Max     int
	Message string
	Commit  string
	// Duration and CostUSD are set on task_done; CostUSD only when reported.
	Duration   time.Duration
	CostUSD    float64
	RecordPath string
}

// AttemptInfo describes an attempt when it begins.
type AttemptInfo struct {
	Slice, Task string
	Attempt     int
	Harness     string
	Model       string
	Mode        harness.PermissionMode
	StartHead   string
	Started     time.Time
}

// Recorder keeps the record of attempts. Its errors never fail an attempt.
type Recorder interface {
	Begin(ctx context.Context, info AttemptInfo) (AttemptRecord, error)
}

// AttemptRecord is the record of one attempt.
type AttemptRecord interface {
	Prompt(text string) error
	// Stdout receives the harness's standard output as it arrives.
	Stdout() io.Writer
	Verification(report verify.Report) error
	// Finish completes the record and returns where it was written.
	Finish(res AttemptResult, run headless.Result) (path string, err error)
}

type noopRecorder struct{}

func (noopRecorder) Begin(context.Context, AttemptInfo) (AttemptRecord, error) {
	return noopRecord{}, nil
}

type noopRecord struct{}

func (noopRecord) Prompt(string) error                                   { return nil }
func (noopRecord) Stdout() io.Writer                                     { return io.Discard }
func (noopRecord) Verification(verify.Report) error                      { return nil }
func (noopRecord) Finish(AttemptResult, headless.Result) (string, error) { return "", nil }
