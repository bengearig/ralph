package opencode_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/harness/opencode"
	"github.com/bengearig/ralph/internal/workflow"
	"go.yaml.in/yaml/v3"
)

func realInputs(t *testing.T) harness.Inputs {
	t.Helper()
	all, err := workflow.All()
	if err != nil {
		t.Fatal(err)
	}
	return harness.Inputs{Instructions: all, MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}}
}

func artifacts(t *testing.T, in harness.Inputs) []harness.Artifact {
	t.Helper()
	arts, err := opencode.New().Artifacts(in)
	if err != nil {
		t.Fatal(err)
	}
	return arts
}

func commandsOf(arts []harness.Artifact) []harness.Artifact {
	var out []harness.Artifact
	for _, a := range arts {
		if a.Kind == harness.KindFile {
			out = append(out, a)
		}
	}
	return out
}

func sectionOf(t *testing.T, arts []harness.Artifact) harness.Artifact {
	t.Helper()
	for _, a := range arts {
		if a.Kind == harness.KindSection {
			return a
		}
	}
	t.Fatal("no section artifact")
	return harness.Artifact{}
}

func oneCommand(t *testing.T, ins workflow.Instruction, server string) string {
	t.Helper()
	in := harness.Inputs{Instructions: []workflow.Instruction{ins}, MCP: harness.MCPServer{Name: server}}
	cmds := commandsOf(artifacts(t, in))
	if len(cmds) != 1 {
		t.Fatalf("%d commands", len(cmds))
	}
	return string(cmds[0].Content)
}

func split(t *testing.T, content string) (head, rest string) {
	t.Helper()
	s, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		t.Fatalf("no opening ---: %q", content)
	}
	head, rest, ok = strings.Cut(s, "\n---\n")
	if !ok {
		t.Fatalf("no closing ---: %q", content)
	}
	return head, rest
}

func TestOpenCodeCommandsOnePerInstruction(t *testing.T) {
	in := realInputs(t)
	cmds := commandsOf(artifacts(t, in))
	if len(cmds) != len(in.Instructions) {
		t.Fatalf("%d commands for %d instructions", len(cmds), len(in.Instructions))
	}
	for i, ins := range in.Instructions {
		a := cmds[i]
		if a.Path != ".opencode/commands/ralph/"+ins.Name+".md" || a.Scope != harness.ScopeProject ||
			a.Kind != harness.KindFile || a.Label != "command /ralph/"+ins.Name {
			t.Errorf("artifact %d = %+v", i, a)
		}
	}
}

func TestOpenCodeCommandFrontMatterOnlyDescription(t *testing.T) {
	for _, desc := range []string{"plain", `say "hi": it's a: test`} {
		content := oneCommand(t, workflow.Instruction{Name: "x", Description: desc, Body: "body\n"}, "ralph")
		head, _ := split(t, content)
		var m map[string]any
		if err := yaml.Unmarshal([]byte(head), &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != 1 || m["description"] != desc {
			t.Errorf("front matter = %v, want only description %q", m, desc)
		}
	}
}

func TestOpenCodeCommandDescriptionIncludesHint(t *testing.T) {
	cases := []struct{ hint, want string }{
		{"[unit id]", "Show status [unit id]"},
		{"", "Show status"},
	}
	for _, c := range cases {
		content := oneCommand(t, workflow.Instruction{Name: "x", Description: "Show status", ArgumentHint: c.hint, Body: "b\n"}, "ralph")
		head, _ := split(t, content)
		var m map[string]string
		if err := yaml.Unmarshal([]byte(head), &m); err != nil {
			t.Fatal(err)
		}
		if m["description"] != c.want {
			t.Errorf("description = %q, want %q", m["description"], c.want)
		}
	}
}

func TestOpenCodeCommandPreface(t *testing.T) {
	for _, server := range []string{"ralph", "other"} {
		content := oneCommand(t, workflow.Instruction{Name: "x", Description: "d", Body: "body\n"}, server)
		_, rest := split(t, content)
		want := "\nThe tools named below are provided by the MCP server \"" + server + "\".\n\nbody\n"
		if rest != want {
			t.Errorf("after front matter = %q, want %q", rest, want)
		}
	}
}

func TestOpenCodeCommandBodyUsesBareToolsAndArguments(t *testing.T) {
	quick, ok, err := workflow.Get("quick")
	if err != nil || !ok {
		t.Fatalf("quick: %v, %v", ok, err)
	}
	content := oneCommand(t, quick, "ralph")
	if !strings.Contains(content, "quick_start") || !strings.Contains(content, "$ARGUMENTS") {
		t.Errorf("missing quick_start or $ARGUMENTS:\n%s", content)
	}
	for _, bad := range []string{"mcp__", "ralph_quick_start", "{{"} {
		if strings.Contains(content, bad) {
			t.Errorf("contains %q", bad)
		}
	}
}

func TestQuickInstructionTicket(t *testing.T) {
	quick, ok, err := workflow.Get("quick")
	if err != nil || !ok {
		t.Fatalf("quick: %v, %v", ok, err)
	}
	content := oneCommand(t, quick, "ralph")
	for _, want := range []string{"`issue_get`", "`quick_start`", "`issue`", "ticket key"} {
		if !strings.Contains(content, want) {
			t.Errorf("content lacks %q:\n%s", want, content)
		}
	}
	for _, bad := range []string{"mcp__", "ralph_issue_get"} {
		if strings.Contains(content, bad) {
			t.Errorf("contains %q", bad)
		}
	}
}

func TestOpenCodeCommandEndsWithNewline(t *testing.T) {
	for _, body := range []string{"no newline", "newline\n"} {
		content := oneCommand(t, workflow.Instruction{Name: "x", Description: "d", Body: body}, "ralph")
		if !strings.HasSuffix(content, "\n") {
			t.Errorf("body %q: %q", body, content)
		}
	}
}

type viewCase struct {
	name string
	fsys fs.FS
	want string
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func viewCases() []viewCase {
	dir := &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	return []viewCase{
		{"empty", fstest.MapFS{}, "AGENTS.md"},
		{"only CLAUDE.md", fstest.MapFS{"CLAUDE.md": file("x")}, "CLAUDE.md"},
		{"only AGENTS.md", fstest.MapFS{"AGENTS.md": file("x")}, "AGENTS.md"},
		{"both", fstest.MapFS{"AGENTS.md": file("x"), "CLAUDE.md": file("y")}, "AGENTS.md"},
		{"AGENTS.md directory", fstest.MapFS{"AGENTS.md": dir, "CLAUDE.md": file("y")}, "CLAUDE.md"},
		{"nil", nil, "AGENTS.md"},
	}
}

func TestOpenCodeSectionPathChoice(t *testing.T) {
	for _, c := range viewCases() {
		t.Run(c.name, func(t *testing.T) {
			in := realInputs(t)
			in.Project = c.fsys
			s := sectionOf(t, artifacts(t, in))
			if s.Path != c.want || s.Scope != harness.ScopeProject {
				t.Errorf("section = %+v, want path %s", s, c.want)
			}
		})
	}
}

func TestOpenCodeSectionContent(t *testing.T) {
	in := realInputs(t)
	if got, want := string(sectionOf(t, artifacts(t, in)).Content), workflow.ProjectNotes(in.Instructions); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	in.ProjectNotes = "custom notes"
	if got := string(sectionOf(t, artifacts(t, in)).Content); got != "custom notes" {
		t.Errorf("override ignored: %q", got)
	}
}

func TestOpenCodeSectionLabelNamesPath(t *testing.T) {
	for _, c := range viewCases() {
		in := realInputs(t)
		in.Project = c.fsys
		if got := sectionOf(t, artifacts(t, in)).Label; got != "section in "+c.want {
			t.Errorf("%s: label = %q", c.name, got)
		}
	}
}

type failingFS struct{ fstest.MapFS }

func (f failingFS) Stat(name string) (fs.FileInfo, error) {
	if name == "CLAUDE.md" {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: errors.New("permission denied")}
	}
	return f.MapFS.Stat(name)
}

func TestOpenCodeSectionStatErrorNamed(t *testing.T) {
	in := realInputs(t)
	in.Project = failingFS{fstest.MapFS{}}
	_, err := opencode.New().Artifacts(in)
	if err == nil || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenCodeArtifactsDeterministic(t *testing.T) {
	for _, c := range viewCases() {
		in := realInputs(t)
		in.Project = c.fsys
		a, b := artifacts(t, in), artifacts(t, in)
		if len(a) != len(b) {
			t.Fatalf("%s: lengths differ", c.name)
		}
		for i := range a {
			if a[i].ID() != b[i].ID() || a[i].Label != b[i].Label || string(a[i].Content) != string(b[i].Content) {
				t.Errorf("%s: artifact %d differs", c.name, i)
			}
		}
	}
}

func TestOpenCodeSharesSectionWithClaudeCode(t *testing.T) {
	for _, c := range viewCases() {
		in := realInputs(t)
		in.Project = c.fsys
		oc := sectionOf(t, artifacts(t, in))
		if oc.Path != "CLAUDE.md" {
			continue
		}
		cc, err := claudecode.New().Artifacts(in)
		if err != nil {
			t.Fatal(err)
		}
		s := sectionOf(t, cc)
		if s.ID() != oc.ID() || s.Path != oc.Path || string(s.Content) != string(oc.Content) {
			t.Errorf("%s: sections differ: %+v vs %+v", c.name, s, oc)
		}
	}
}
