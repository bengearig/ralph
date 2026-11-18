package prstep

import (
	"strings"

	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/verify"
)

const commitLength = 12

// BodyTask is a finished task listed in the pull request body.
type BodyTask struct {
	ID, Title string
	// Commit is the full hash from the task's commit link, empty when it has none.
	Commit string
}

// BodyInput is what the pull request body is built from.
type BodyInput struct {
	SliceID   string
	SliceBody string
	// BriefID and BriefTitle are empty for a slice with no brief.
	BriefID, BriefTitle string
	Tasks               []BodyTask
	Report              verify.Report
	// Configured is whether verification commands are configured.
	Configured bool
	// Issue is the ticket the slice or its brief was started from, nil when none.
	Issue *quick.BodyIssue
}

// BuildBody builds the pull request description of a slice.
func BuildBody(in BodyInput) string {
	var sections []string
	add := func(s string) {
		if s = strings.TrimRight(s, "\r\n"); strings.TrimSpace(s) != "" {
			sections = append(sections, s)
		}
	}
	if in.BriefID != "" {
		add("Part of " + in.BriefID + ": " + in.BriefTitle + ".")
	}
	add(in.SliceBody)
	if len(in.Tasks) > 0 {
		lines := []string{"## Tasks", ""}
		for _, t := range in.Tasks {
			line := "- " + t.ID + ": " + t.Title
			if t.Commit != "" {
				line += " (" + shortCommit(t.Commit) + ")"
			}
			lines = append(lines, line)
		}
		add(strings.Join(lines, "\n"))
	}
	add(verificationSection(in))
	if in.Issue != nil && in.Issue.Key != "" {
		add(quick.IssueLine(*in.Issue))
	}
	add("Opened by ralph for " + in.SliceID + ".")
	return strings.Join(sections, "\n\n") + "\n"
}

func shortCommit(hash string) string {
	if len(hash) > commitLength {
		return hash[:commitLength]
	}
	return hash
}

// verificationSection reuses the wording of quick work's body by building it
// with no ID and cutting the closing line off.
func verificationSection(in BodyInput) string {
	full := quick.BuildPullRequestBody(quick.BodyInput{Report: in.Report, Configured: in.Configured})
	return strings.TrimSpace(strings.TrimSuffix(full, "Opened by ralph for .\n"))
}
