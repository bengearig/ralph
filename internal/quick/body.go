package quick

import (
	"fmt"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/verify"
)

const noVerification = "No verification commands are configured for this project."

// BodyInput is what the pull request body is built from.
type BodyInput struct {
	ID                 string
	Body               string
	AcceptanceCriteria string
	Summary            string
	Report             verify.Report
	// Configured is whether verification commands are configured.
	Configured bool
}

// BuildPullRequestBody builds the pull request description.
func BuildPullRequestBody(in BodyInput) string {
	var sections []string
	add := func(s string) {
		if s = strings.TrimRight(s, "\r\n"); strings.TrimSpace(s) != "" {
			sections = append(sections, s)
		}
	}
	add(in.Body)
	if strings.TrimSpace(in.AcceptanceCriteria) != "" {
		add("## Acceptance criteria\n\n" + strings.TrimRight(in.AcceptanceCriteria, "\r\n"))
	}
	if strings.TrimSpace(in.Summary) != "" {
		add("## What changed\n\n" + strings.TrimRight(in.Summary, "\r\n"))
	}
	switch {
	case !in.Configured:
		add("## Verification\n\n" + noVerification)
	case len(in.Report.Results) > 0:
		lines := []string{"## Verification", ""}
		for _, r := range in.Report.Results {
			lines = append(lines, fmt.Sprintf("- %s: passed (%s)", r.Command, r.Duration.Round(100*time.Millisecond)))
		}
		add(strings.Join(lines, "\n"))
	}
	add("Opened by ralph for " + in.ID + ".")
	return strings.Join(sections, "\n\n") + "\n"
}
