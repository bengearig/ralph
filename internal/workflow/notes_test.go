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

func TestProjectNotesListsReceive(t *testing.T) {
	got, _ := notesFor(t)
	for _, want := range []string{"/ralph:receive", "/ralph/receive"} {
		if !strings.Contains(got, want) {
			t.Errorf("notes lack %q:\n%s", want, got)
		}
	}
}

func TestProjectNotesListsArticulate(t *testing.T) {
	got, _ := notesFor(t)
	for _, want := range []string{"/ralph:articulate", "/ralph/articulate"} {
		if !strings.Contains(got, want) {
			t.Errorf("notes lack %q:\n%s", want, got)
		}
	}
}

func TestProjectNotesMentionDocumentsAndSearch(t *testing.T) {
	got, _ := notesFor(t)
	paras := strings.Split(got, "\n\n")
	levels := -1
	for i, p := range paras {
		if strings.HasPrefix(p, "Work has three levels") {
			levels = i
		}
	}
	if levels < 0 || levels+1 >= len(paras) {
		t.Fatalf("no paragraph after the levels paragraph:\n%s", got)
	}
	next := paras[levels+1]
	for _, want := range []string{"`search`", "`doc_`", "`link_add`", "kind `document`"} {
		if !strings.Contains(next, want) {
			t.Errorf("paragraph after the levels lacks %q:\n%s", want, next)
		}
	}
}

func TestProjectNotesStillMentionNotices(t *testing.T) {
	got, _ := notesFor(t)
	if !strings.Contains(got, "Notice for the user:") {
		t.Errorf("notes lack the notice line:\n%s", got)
	}
}
