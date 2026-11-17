package headless

import (
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func TestFailureOrder(t *testing.T) {
	denials := func(n int) []harness.Denial {
		var d []harness.Denial
		for _, t := range []string{"a", "b", "c", "d", "e"}[:n] {
			d = append(d, harness.Denial{Tool: "Bash", Target: "cmd " + t})
		}
		return d
	}
	finished := harness.Outcome{Finished: true}
	tests := []struct {
		name string
		res  Result
		want string
	}{
		{"clean", Result{Outcome: finished}, ""},
		{"canceled", Result{Canceled: true, TimedOut: true, BudgetStopped: true, ExitCode: 1}, "canceled"},
		{"timed out", Result{TimedOut: true, BudgetStopped: true, ExitCode: 1}, "timed out"},
		{"budget stopped", Result{BudgetStopped: true, ExitCode: 1, Outcome: harness.Outcome{Error: "x"}}, "budget exceeded"},
		{"budget reported", Result{Outcome: harness.Outcome{BudgetExceeded: true, Error: "x"}}, "budget exceeded"},
		{"harness error", Result{ExitCode: 1, Outcome: harness.Outcome{Error: "bad\nthing", Denials: denials(1)}}, "harness error: bad thing"},
		{"denied one", Result{ExitCode: 1, Outcome: harness.Outcome{Finished: true, Denials: denials(1)}}, "denied actions (1): Bash cmd a"},
		{"denied three", Result{Outcome: harness.Outcome{Finished: true, Denials: denials(3)}}, "denied actions (3): Bash cmd a; Bash cmd b; Bash cmd c"},
		{"denied five", Result{Outcome: harness.Outcome{Finished: true, Denials: denials(5)}}, "denied actions (5): Bash cmd a; Bash cmd b; Bash cmd c; and 2 more"},
		{"exit status", Result{ExitCode: 3, Outcome: finished}, "exit status 3"},
		{"signal", Result{ExitCode: -1}, "exit status -1"},
		{"unfinished", Result{}, "the harness ended without reporting a result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.Failure(); got != tt.want {
				t.Errorf("Failure() = %q, want %q", got, tt.want)
			}
		})
	}
}
