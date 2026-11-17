package headless

import (
	"fmt"

	"github.com/bengearig/ralph/internal/harness"
)

// Suggest is the line a user can act on to allow a denied action.
func Suggest(d harness.Denial) string {
	switch {
	case d.Shell && d.Target != "":
		return fmt.Sprintf("to allow this command, add \"%s\" to headless.allow (end the pattern with \" *\" to allow any arguments)", EscapePattern(d.Target))
	case d.Shell:
		return "the command is outside the restricted allowlist; add a pattern for it to headless.allow"
	}
	return d.Tool + " is outside the restricted allowlist; set headless.permissions to auto to let the harness decide"
}

// Suggestions are the texts for the denials of the run, without duplicates.
func (r Result) Suggestions() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, d := range r.Outcome.Denials {
		s := Suggest(d)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
