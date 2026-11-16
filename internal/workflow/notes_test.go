package workflow

import (
	"strings"
	"testing"
)

func notesFor(t *testing.T) (string, []Instruction) {
	t.Helper()
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	return ProjectNotes(all), all
}

func TestProjectNotesBareToolNames(t *testing.T) {
	got, _ := notesFor(t)
	for _, want := range []string{"`unit_get`", "`status`", "Notice for the user:", "MCP server named ralph"} {
		if !strings.Contains(got, want) {
			t.Errorf("notes lack %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"mcp__", "CLAUDE.md", "AGENTS.md"} {
		if strings.Contains(got, bad) {
			t.Errorf("notes contain %q:\n%s", bad, got)
		}
	}
}

func TestProjectNotesBothCommandStyles(t *testing.T) {
	got, all := notesFor(t)
	for _, in := range all {
		for _, want := range []string{"/ralph:" + in.Name, "/ralph/" + in.Name} {
			if !strings.Contains(got, want) {
				t.Errorf("notes lack %q:\n%s", want, got)
			}
		}
	}
}

func TestProjectNotesStillMentionsEssentials(t *testing.T) {
	got, _ := notesFor(t)
	for _, w := range []string{"fingerprint", "orphan task", ".ralph"} {
		if !strings.Contains(got, w) {
			t.Errorf("notes lack %q:\n%s", w, got)
		}
	}
}
