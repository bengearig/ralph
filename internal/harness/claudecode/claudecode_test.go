package claudecode

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/harnesstest"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"
)

func inputs(t *testing.T) harness.Inputs {
	t.Helper()
	all, err := workflow.All()
	if err != nil {
		t.Fatal(err)
	}
	return harness.Inputs{Instructions: all, MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}}
}

func statusArtifact(t *testing.T) harness.Artifact {
	t.Helper()
	arts, err := New().Artifacts(inputs(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Path == ".claude/commands/ralph/status.md" {
			return a
		}
	}
	t.Fatal("no status command artifact")
	return harness.Artifact{}
}

func frontMatter(t *testing.T, content []byte) (head, rest string) {
	t.Helper()
	s, ok := strings.CutPrefix(string(content), "---\n")
	if !ok {
		t.Fatalf("no opening ---: %q", content)
	}
	head, rest, ok = strings.Cut(s, "\n---\n")
	if !ok {
		t.Fatalf("no closing ---: %q", content)
	}
	return head, rest
}

func TestArtifactsForStatus(t *testing.T) {
	in := inputs(t)
	arts, err := New().Artifacts(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != len(in.Instructions) {
		t.Fatalf("%d artifacts for %d instructions", len(arts), len(in.Instructions))
	}
	a := statusArtifact(t)
	if a.Kind != harness.KindFile || a.Scope != harness.ScopeProject || a.Label != "command /ralph:status" {
		t.Errorf("artifact = %+v", a)
	}
}

func TestCommandFrontMatterParses(t *testing.T) {
	status, _, _ := workflow.Get("status")
	cases := []workflow.Instruction{
		status,
		{Name: "tricky", Description: `say "hi": it's a: test`, ArgumentHint: `[a: "b"]`, Body: "body\n"},
		{Name: "nohint", Description: "plain", Body: "body\n"},
	}
	for _, ins := range cases {
		t.Run(ins.Name, func(t *testing.T) {
			content, err := command(ins, "ralph")
			if err != nil {
				t.Fatal(err)
			}
			head, _ := frontMatter(t, content)
			var got struct {
				Description  string `yaml:"description"`
				ArgumentHint string `yaml:"argument-hint"`
			}
			if err := yaml.Unmarshal([]byte(head), &got); err != nil {
				t.Fatal(err)
			}
			if got.Description != ins.Description || got.ArgumentHint != ins.ArgumentHint {
				t.Errorf("parsed %+v, want %q / %q", got, ins.Description, ins.ArgumentHint)
			}
			if ins.ArgumentHint == "" && strings.Contains(head, "argument-hint") {
				t.Error("empty argument hint was written")
			}
		})
	}
}

func TestCommandBodyUsesClaudeNames(t *testing.T) {
	_, body := frontMatter(t, statusArtifact(t).Content)
	for _, want := range []string{"mcp__ralph__status", "mcp__ralph__unit_get", "$ARGUMENTS"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Error("body has an unrendered template action")
	}
	if !strings.HasPrefix(body, "\n") {
		t.Error("no blank line between front matter and body")
	}
}

func TestCommandEndsWithNewline(t *testing.T) {
	c := statusArtifact(t).Content
	if !bytes.HasSuffix(c, []byte("\n")) {
		t.Error("content does not end with a newline")
	}
	nl, err := command(workflow.Instruction{Name: "x", Description: "d", Body: "no newline"}, "ralph")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(nl, []byte("\n")) {
		t.Error("body without trailing newline was not terminated")
	}
}

func TestToolNamesExistOnTheServer(t *testing.T) {
	ctx := context.Background()
	srv := mcpserver.New(mcpserver.Options{
		Name:    "ralph",
		Version: "test",
		Open:    func(context.Context) (units.Service, error) { return nil, nil },
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range res.Tools {
		have[tool.Name] = true
	}
	for _, ins := range inputs(t).Instructions {
		for _, tool := range ins.Tools() {
			if !have[tool] {
				t.Errorf("instruction %s references tool %q, which the server does not register", ins.Name, tool)
			}
		}
	}
}

func TestAdapterConformance(t *testing.T) {
	harnesstest.Check(t, New())
}
