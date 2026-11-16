package quick

import (
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/verify"
)

func TestBuildPullRequestBodyAllSections(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{
		ID: "T-1", Body: "The body.", AcceptanceCriteria: "- it works", Summary: "Changed things.",
		Report: verify.Report{Results: []verify.Result{
			{Command: "make test", Duration: 1234 * time.Millisecond},
			{Command: "make lint", Duration: 40 * time.Millisecond},
		}},
		Configured: true,
	})
	want := "The body.\n\n## Acceptance criteria\n\n- it works\n\n## What changed\n\nChanged things.\n\n" +
		"## Verification\n\n- make test: passed (1.2s)\n- make lint: passed (0s)\n\nOpened by ralph for T-1.\n"
	if got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildPullRequestBodyOmitsEmptySections(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{ID: "T-2", Configured: true})
	if got != "Opened by ralph for T-2.\n" {
		t.Fatalf("body = %q", got)
	}
	got = BuildPullRequestBody(BodyInput{ID: "T-2", Body: "b", Summary: "s", Configured: true})
	if strings.Contains(got, "Acceptance criteria") || strings.Contains(got, "Verification") || !strings.Contains(got, "## What changed") {
		t.Fatalf("body = %q", got)
	}
}

func TestBuildPullRequestBodyNoVerification(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{ID: "T-3", Body: "b"})
	want := "b\n\n## Verification\n\nNo verification commands are configured for this project.\n\nOpened by ralph for T-3.\n"
	if got != want {
		t.Fatalf("body = %q", got)
	}
}

func TestBuildPullRequestBodyEndsWithNewline(t *testing.T) {
	for _, in := range []BodyInput{{ID: "T-1"}, {ID: "T-1", Body: "b\n\n", Summary: "s\n"}} {
		got := BuildPullRequestBody(in)
		if !strings.HasSuffix(got, "Opened by ralph for T-1.\n") || strings.HasSuffix(got, "\n\n") || strings.Contains(got, "\n\n\n") {
			t.Fatalf("body = %q", got)
		}
	}
}
