package integration

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/workflow"
)

func TestProjectNotesAskBeforeNoticeDecision(t *testing.T) {
	instructions, err := workflow.All()
	if err != nil {
		t.Fatal(err)
	}
	in := harness.Inputs{Instructions: instructions, MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}}
	const want = "A notice can ask for a decision: relay it, ask the user, and do nothing until they answer"
	for _, name := range []string{"claude-code", "opencode"} {
		a, ok := all.ByName(name)
		if !ok {
			t.Fatalf("no adapter %q", name)
		}
		arts, err := a.Artifacts(in)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, art := range arts {
			if art.Kind == harness.KindSection && strings.Contains(string(art.Content), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the rendered notes lack %q", name, want)
		}
	}
}

func TestProjectNotesMentionTicketsAndAskingFirst(t *testing.T) {
	instructions, err := workflow.All()
	if err != nil {
		t.Fatal(err)
	}
	in := harness.Inputs{Instructions: instructions, MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}}
	const want = "Ralph can mirror units to Jira tickets and keeps ralph's values; if a notice says tickets differ or failed, tell the user and let them choose between `ralph tickets accept` and `ralph tickets overwrite`."
	for _, name := range []string{"claude-code", "opencode"} {
		a, ok := all.ByName(name)
		if !ok {
			t.Fatalf("no adapter %q", name)
		}
		arts, err := a.Artifacts(in)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, art := range arts {
			if art.Kind == harness.KindSection && strings.Contains(string(art.Content), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the rendered notes lack %q", name, want)
		}
	}
}
