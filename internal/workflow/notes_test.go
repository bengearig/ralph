package workflow

import (
	"strings"
	"testing"
)

func TestProjectNotesMentionsEssentials(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	got := ProjectNotes(all, func(n string) string { return n })
	want := []string{"fingerprint", "Notice for the user:", "orphan task", ".ralph"}
	for _, in := range all {
		want = append(want, "/ralph:"+in.Name)
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("notes lack %q:\n%s", w, got)
		}
	}
}

func TestProjectNotesUseToolFunction(t *testing.T) {
	got := ProjectNotes(nil, func(n string) string { return "<<" + n + ">>" })
	if !strings.Contains(got, "<<status>>") || !strings.Contains(got, "<<unit_get>>") {
		t.Errorf("tool names not routed through the function:\n%s", got)
	}
	if strings.Contains(got, "`status`") {
		t.Errorf("bare tool name leaked:\n%s", got)
	}
}

func TestProjectNotesHarnessNeutral(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	got := ProjectNotes(all, func(n string) string { return n })
	for _, bad := range []string{"mcp__", "CLAUDE"} {
		if strings.Contains(got, bad) {
			t.Errorf("notes contain %q", bad)
		}
	}
}
