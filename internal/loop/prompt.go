// Package loop runs the tasks of a slice one at a time and records each run.
package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/units"
)

const (
	maxPromptNotes    = 20
	maxPromptNoteSize = 2000
	reasonOverLimit   = "over the prompt limit"
)

// PromptDeps are the services a prompt is built from.
type PromptDeps struct {
	Units units.Service
	Links *doclinks.Service
	Docs  documents.Service
}

// Failure is the previous attempt. VerificationOutput is the text to show.
type Failure struct {
	Line               string
	Denials            []harness.Denial
	VerificationOutput string
}

// PromptInput describes the prompt for one attempt at one task. A zero
// DocumentBytes puts no limit on linked-document text.
type PromptInput struct {
	TaskID        string
	Verify        []string
	Attempt       int
	MaxAttempts   int
	Previous      *Failure
	DocumentBytes int
}

// DocumentUse says what became of one considered document.
type DocumentUse struct {
	ID       string
	Title    string
	From     string
	Included bool
	Reason   string
}

// Prompt is the text for one attempt and the documents it considered.
type Prompt struct {
	Text      string
	Documents []DocumentUse
}

// BuildPrompt makes the prompt for a task that belongs to a slice.
func BuildPrompt(ctx context.Context, d PromptDeps, in PromptInput) (Prompt, error) {
	task, err := d.Units.Get(ctx, in.TaskID)
	if err != nil {
		return Prompt{}, err
	}
	if task.Level != units.LevelTask {
		return Prompt{}, fmt.Errorf("%s is a %s; the loop works tasks", task.ID, task.Level)
	}
	if task.ParentID == "" {
		return Prompt{}, fmt.Errorf("%s has no slice; the loop works the tasks of a slice", task.ID)
	}
	slice, err := d.Units.Get(ctx, task.ParentID)
	if err != nil {
		return Prompt{}, err
	}
	var brief *units.Unit
	if slice.ParentID != "" {
		b, err := d.Units.Get(ctx, slice.ParentID)
		if err != nil {
			return Prompt{}, err
		}
		brief = &b
	}
	siblings, err := d.Units.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &slice.ID})
	if err != nil {
		return Prompt{}, err
	}
	notes, err := d.Units.Notes(ctx, task.ID)
	if err != nil {
		return Prompt{}, err
	}
	uses, docSection, err := documentsSection(ctx, d, task, slice, brief, in.DocumentBytes)
	if err != nil {
		return Prompt{}, err
	}

	sections := []string{
		header(task),
		"## The task\n\n" + strings.TrimSpace(task.Body),
		"## Acceptance criteria\n\n" + criteria(task.AcceptanceCriteria),
		fitsSection(task, slice, brief, siblings),
	}
	if s := notesSection(notes); s != "" {
		sections = append(sections, s)
	}
	if docSection != "" {
		sections = append(sections, docSection)
	}
	sections = append(sections, verifySection(in.Verify))
	if in.Attempt > 1 && in.Previous != nil {
		sections = append(sections, failureSection(in))
	}
	return Prompt{Text: strings.Join(sections, "\n\n") + "\n", Documents: uses}, nil
}

func header(task units.Unit) string {
	return strings.Join([]string{
		fmt.Sprintf("# Task %s: %s", task.ID, task.Title),
		"You are one step in a larger plan. Do this one task and nothing else.",
		"- Make the changes in the working tree. Do not commit, push, or switch or create branches: ralph checks the result and commits it.\n" +
			"- Do not edit files under .git or .ralph.\n" +
			"- If the task cannot be done, or is already done, say so plainly in your final answer instead of making unrelated changes.\n" +
			"- You can look up more with the ralph tools: search finds work and documents, and doc_get reads a document.",
	}, "\n\n")
}

func criteria(c string) string {
	if strings.TrimSpace(c) == "" {
		return "(none recorded)"
	}
	return strings.TrimSpace(c)
}

func fitsSection(task, slice units.Unit, brief *units.Unit, siblings []units.Unit) string {
	parts := []string{"## Where this fits"}
	if brief != nil {
		parts = append(parts, unitBlock("Brief", *brief))
	}
	parts = append(parts, unitBlock("Slice", slice))
	var b strings.Builder
	b.WriteString("Tasks in this slice (for context only; do not do them):\n")
	for i, t := range siblings {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s %s %s", t.ID, t.State, t.Title)
		if t.ID == task.ID {
			b.WriteString(" <- this task")
		}
	}
	parts = append(parts, b.String())
	return strings.Join(parts, "\n\n")
}

func unitBlock(label string, u units.Unit) string {
	s := fmt.Sprintf("%s %s: %s", label, u.ID, u.Title)
	if body := strings.TrimSpace(u.Body); body != "" {
		s += "\n\n" + body
	}
	return s
}

func notesSection(notes []units.Note) string {
	if len(notes) == 0 {
		return ""
	}
	if len(notes) > maxPromptNotes {
		notes = notes[len(notes)-maxPromptNotes:]
	}
	lines := make([]string, len(notes))
	for i, n := range notes {
		body := n.Body
		if r := []rune(body); len(r) > maxPromptNoteSize {
			body = string(r[:maxPromptNoteSize]) + "…"
		}
		lines[i] = fmt.Sprintf("- %s %s: %s", n.Kind, n.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"), body)
	}
	return "## Notes on this task\n\n" + strings.Join(lines, "\n")
}

func documentsSection(ctx context.Context, d PromptDeps, task, slice units.Unit, brief *units.Unit, limit int) ([]DocumentUse, string, error) {
	direct, inherited, err := d.Links.DocumentsOf(ctx, task.ID)
	if err != nil {
		return nil, "", err
	}
	var (
		uses     []DocumentUse
		included []string
		skipped  []string
		used     int
	)
	for _, group := range [][]doclinks.Linked{direct, inherited} {
		for _, l := range group {
			if l.Status != string(documents.StatusActive) {
				continue
			}
			doc, err := d.Docs.Get(ctx, l.ID)
			if err != nil {
				return nil, "", fmt.Errorf("read document %s: %w", l.ID, err)
			}
			use := DocumentUse{ID: l.ID, Title: l.Title, From: l.From}
			if limit > 0 && used+len(doc.Body) > limit {
				use.Reason = reasonOverLimit
				skipped = append(skipped, fmt.Sprintf("- %s %s: %s. Read it with doc_get.", l.ID, l.Title, reasonOverLimit))
			} else {
				use.Included = true
				used += len(doc.Body)
				included = append(included, fmt.Sprintf("### %s %s: %s (linked to %s)\n\n%s",
					l.ID, l.Kind, l.Title, linkedTo(l.From, slice, brief), strings.TrimRight(doc.Body, "\n")))
			}
			uses = append(uses, use)
		}
	}
	if len(uses) == 0 {
		return uses, "", nil
	}
	parts := append([]string{"## Documents"}, included...)
	if len(skipped) > 0 {
		parts = append(parts, "Not included:\n"+strings.Join(skipped, "\n"))
	}
	return uses, strings.Join(parts, "\n\n"), nil
}

func linkedTo(from string, slice units.Unit, brief *units.Unit) string {
	switch {
	case from == "":
		return "this task"
	case from == slice.ID:
		return "slice " + from
	case brief != nil && from == brief.ID:
		return "brief " + from
	}
	return from
}

func verifySection(commands []string) string {
	if len(commands) == 0 {
		return "## How your work is checked\n\nRalph has no verification commands configured for this project."
	}
	lines := make([]string, len(commands))
	for i, c := range commands {
		lines[i] = "- `" + c + "`"
	}
	return "## How your work is checked\n\nRalph runs these commands after you finish, from the project root. All of them must pass:\n\n" +
		strings.Join(lines, "\n")
}

func failureSection(in PromptInput) string {
	p := in.Previous
	parts := []string{
		fmt.Sprintf("## The previous attempt failed (attempt %d of %d)", in.Attempt-1, in.MaxAttempts),
		p.Line,
	}
	if len(p.Denials) > 0 {
		lines := []string{"Denied actions:"}
		for _, dn := range p.Denials {
			line := "- " + dn.Tool
			if dn.Target != "" {
				line += " " + dn.Target
			}
			lines = append(lines, line+": "+dn.Reason)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if p.VerificationOutput != "" {
		fence := strings.Repeat("`", max(3, longestBacktickRun(p.VerificationOutput)+1))
		parts = append(parts, "Output of the failing verification command:\n\n"+
			fence+"\n"+strings.TrimRight(p.VerificationOutput, "\n")+"\n"+fence)
	}
	return strings.Join(parts, "\n\n")
}

func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}
