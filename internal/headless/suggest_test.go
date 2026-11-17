package headless

import (
	"reflect"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func TestSuggestShellDenialQuotesEscapedCommand(t *testing.T) {
	got := Suggest(harness.Denial{Tool: "Bash", Target: `ls *.go`, Shell: true})
	want := `to allow this command, add "ls \*.go" to headless.allow (end the pattern with " *" to allow any arguments)`
	if got != want {
		t.Errorf("Suggest = %q, want %q", got, want)
	}
}

func TestSuggestShellDenialWithoutTarget(t *testing.T) {
	got := Suggest(harness.Denial{Tool: "shell", Shell: true})
	want := "the command is outside the restricted allowlist; add a pattern for it to headless.allow"
	if got != want {
		t.Errorf("Suggest = %q, want %q", got, want)
	}
}

func TestSuggestOtherToolPointsAtAuto(t *testing.T) {
	got := Suggest(harness.Denial{Tool: "WebFetch", Target: "https://x"})
	want := "WebFetch is outside the restricted allowlist; set headless.permissions to auto to let the harness decide"
	if got != want {
		t.Errorf("Suggest = %q, want %q", got, want)
	}
}

func TestSuggestionsDedupeAndOrder(t *testing.T) {
	r := Result{Outcome: harness.Outcome{Denials: []harness.Denial{
		{Tool: "Write"}, {Tool: "Bash", Target: "ls", Shell: true}, {Tool: "Write", Target: "other"}, {Tool: "Bash", Target: "ls", Shell: true},
	}}}
	want := []string{Suggest(r.Outcome.Denials[0]), Suggest(r.Outcome.Denials[1])}
	if got := r.Suggestions(); !reflect.DeepEqual(got, want) {
		t.Errorf("Suggestions = %q, want %q", got, want)
	}
}

func TestSuggestionsEmptyNonNil(t *testing.T) {
	if got := (Result{}).Suggestions(); got == nil || len(got) != 0 {
		t.Errorf("Suggestions = %#v", got)
	}
}
