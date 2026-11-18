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
	// Issue is the ticket the work was started from, nil when none.
	Issue *BodyIssue
}

// BodyIssue is the ticket a pull request body refers to.
type BodyIssue struct {
	Key string
	// URL is empty when none can be built.
	URL string
}

// IssueLine is the line of a pull request body that names the ticket.
func IssueLine(i BodyIssue) string {
	if i.URL == "" {
		return "Issue: " + i.Key
	}
	return "Issue: " + i.Key + " " + i.URL
}

// IssueURL is the address of a ticket on a Jira site, empty when site is empty.
func IssueURL(site, key string) string {
	if site = strings.TrimRight(site, "/"); site == "" {
		return ""
	}
	return site + "/browse/" + key
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
	if in.Issue != nil && in.Issue.Key != "" {
		add(IssueLine(*in.Issue))
	}
	add("Opened by ralph for " + in.ID + ".")
	return strings.Join(sections, "\n\n") + "\n"
}
