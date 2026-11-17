package harness

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode"
)

// PermissionMode is how much a headless run may do without asking.
type PermissionMode string

const (
	// PermissionRestricted denies everything except a short allowlist.
	PermissionRestricted PermissionMode = "restricted"
	// PermissionAuto defers to the harness's own automatic mode.
	PermissionAuto PermissionMode = "auto"
)

// Valid reports whether m is a known mode. The empty mode is not valid here;
// a request treats it as restricted.
func (m PermissionMode) Valid() bool {
	return m == PermissionRestricted || m == PermissionAuto
}

// CheckMode fails when req asks for the automatic mode and a has none.
func CheckMode(a Adapter, req HeadlessRequest) error {
	if req.Mode == PermissionAuto && !a.Capabilities().Auto {
		return fmt.Errorf("%s has no automatic mode; set headless.permissions to restricted", a.Name())
	}
	return nil
}

// Allowlist is what a restricted run may do beyond editing project files.
type Allowlist struct {
	// Tools are ralph tools by bare name.
	Tools []string
	// Commands are shell command patterns: `*` matches any text, a backslash
	// takes the next character literally, and a trailing space and `*` also
	// matches the command with no arguments.
	Commands []string
	// Protected are project-relative paths the agent must not edit.
	Protected []string
}

// HeadlessRequest describes one non-interactive run of a harness.
type HeadlessRequest struct {
	// Dir is the project root and the working directory.
	Dir    string
	Prompt string
	// Model is empty for the harness default.
	Model string
	// Version is the detected harness version, empty when unknown.
	Version string
	// Mode is empty for restricted.
	Mode  PermissionMode
	Allow Allowlist
	MCP   MCPServer
	// BudgetUSD caps the cost of the run; 0 means none.
	BudgetUSD float64
}

// Validate reports the first problem with the request.
func (r HeadlessRequest) Validate() error {
	if r.Dir == "" {
		return fmt.Errorf("headless request: Dir is not set")
	}
	if !filepath.IsAbs(r.Dir) {
		return fmt.Errorf("headless request: Dir %q is not an absolute path", r.Dir)
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("headless request: Prompt is empty")
	}
	if r.Mode != "" && !r.Mode.Valid() {
		return fmt.Errorf("headless request: Mode %q is not %q or %q", r.Mode, PermissionRestricted, PermissionAuto)
	}
	if math.IsNaN(r.BudgetUSD) || math.IsInf(r.BudgetUSD, 0) || r.BudgetUSD < 0 {
		return fmt.Errorf("headless request: BudgetUSD %v must be a finite number of at least 0", r.BudgetUSD)
	}
	if r.Model != "" {
		if strings.HasPrefix(r.Model, "-") {
			return fmt.Errorf("headless request: Model %q must not start with %q", r.Model, "-")
		}
		for _, c := range r.Model {
			if unicode.IsSpace(c) || unicode.IsControl(c) {
				return fmt.Errorf("headless request: Model %q must not contain a space or control character", r.Model)
			}
		}
	}
	if r.MCP.Name == "" {
		return fmt.Errorf("headless request: MCP.Name is empty")
	}
	if r.MCP.Command == "" {
		return fmt.Errorf("headless request: MCP.Command is empty")
	}
	return nil
}

// Invocation describes a process as data; an adapter never starts it.
type Invocation struct {
	Command string
	Args    []string
	// Env holds additions to the inherited environment, as KEY=VALUE.
	Env []string
	Dir string
	// Stdin is the prompt; it never travels in Args.
	Stdin string
}

// Usage is what a run consumed.
type Usage struct {
	CostUSD          float64
	CostReported     bool
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
}

// Denial is an action the harness refused.
type Denial struct {
	Tool   string
	Target string
	Reason string
}

// Outcome is the end state of a run as the harness reported it.
type Outcome struct {
	Finished       bool
	Success        bool
	Text           string
	SessionID      string
	Error          string
	BudgetExceeded bool
	Denials        []Denial
	Usage          Usage
}

// Capabilities says what a harness can do on its own.
type Capabilities struct {
	// Auto means the harness has its own automatic permission mode.
	Auto bool
	// NativeBudget means the harness stops itself at a cost cap.
	NativeBudget bool
}

// Parser reads a harness's standard output. A line that is not an event is
// ignored, and Usage and Outcome return what was seen so far, so a truncated
// stream is fine. Line must not retain the slice it is given.
type Parser interface {
	Line([]byte)
	Usage() Usage
	Outcome() Outcome
}
