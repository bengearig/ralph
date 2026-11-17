package headless

import (
	"fmt"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/harness"
)

const listedDenials = 3

// Result is what one run did.
type Result struct {
	// ExitCode is -1 when a signal ended the process.
	ExitCode int
	Duration time.Duration
	TimedOut bool
	Canceled bool
	// BudgetStopped is set when ralph stopped the run at the cost cap.
	BudgetStopped bool
	// SkippedLines counts output lines too long to hand to the parser.
	SkippedLines int
	// Stderr is the last part of standard error.
	Stderr  string
	Notes   []string
	Outcome harness.Outcome
}

// Failure returns an empty string for a clean run, otherwise one line saying
// why it is not.
func (r Result) Failure() string {
	switch {
	case r.Canceled:
		return "canceled"
	case r.TimedOut:
		return "timed out"
	case r.Outcome.BudgetExceeded || r.BudgetStopped:
		return "budget exceeded"
	case r.Outcome.Error != "":
		return "harness error: " + oneLine(r.Outcome.Error)
	case len(r.Outcome.Denials) > 0:
		return denialLine(r.Outcome.Denials)
	case r.ExitCode != 0:
		return fmt.Sprintf("exit status %d", r.ExitCode)
	case !r.Outcome.Finished:
		return "the harness ended without reporting a result"
	}
	return ""
}

func denialLine(denials []harness.Denial) string {
	var listed []string
	for i, d := range denials {
		if i == listedDenials {
			break
		}
		listed = append(listed, oneLine(d.Tool+" "+d.Target))
	}
	line := fmt.Sprintf("denied actions (%d): %s", len(denials), strings.Join(listed, "; "))
	if more := len(denials) - listedDenials; more > 0 {
		line += fmt.Sprintf("; and %d more", more)
	}
	return line
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
