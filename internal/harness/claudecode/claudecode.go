// Package claudecode describes ralph's integration with Claude Code.
package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/workflow"
	"go.yaml.in/yaml/v3"
)

const name = "claude-code"

type adapter struct{}

// New returns the Claude Code adapter.
func New() harness.Adapter { return adapter{} }

func (adapter) Name() string { return name }

func (adapter) Artifacts(in harness.Inputs) ([]harness.Artifact, error) {
	var arts []harness.Artifact
	for _, ins := range in.Instructions {
		content, err := command(ins, in.MCP.Name)
		if err != nil {
			return nil, err
		}
		arts = append(arts, harness.Artifact{
			Kind:    harness.KindFile,
			Scope:   harness.ScopeProject,
			Path:    ".claude/commands/ralph/" + ins.Name + ".md",
			Content: content,
			Label:   "command /ralph:" + ins.Name,
		})
	}
	notes := in.ProjectNotes
	if notes == "" {
		notes = workflow.ProjectNotes(in.Instructions)
	}
	arts = append(arts, harness.Artifact{
		Kind:    harness.KindSection,
		Scope:   harness.ScopeProject,
		Path:    "CLAUDE.md",
		Content: []byte(notes),
		Label:   "section in CLAUDE.md",
	})
	mcp, err := mcpContent(in.MCP)
	if err != nil {
		return nil, err
	}
	perm, err := json.Marshal("mcp__" + in.MCP.Name)
	if err != nil {
		return nil, fmt.Errorf("permission: %w", err)
	}
	arts = append(arts,
		harness.Artifact{
			Kind:    harness.KindJSONEntry,
			Scope:   harness.ScopeProject,
			Path:    ".mcp.json",
			KeyPath: []string{"mcpServers", in.MCP.Name},
			Content: mcp,
			Label:   "MCP server in .mcp.json",
		},
		harness.Artifact{
			Kind:    harness.KindJSONArrayItem,
			Scope:   harness.ScopeLocal,
			Path:    ".claude/settings.local.json",
			KeyPath: []string{"permissions", "allow"},
			Content: perm,
			Label:   "permission in .claude/settings.local.json",
		})
	return arts, nil
}

func mcpContent(m harness.MCPServer) ([]byte, error) {
	args := m.Args
	if args == nil {
		args = []string{}
	}
	b, err := json.Marshal(map[string]any{"type": "stdio", "command": m.Command, "args": args})
	if err != nil {
		return nil, fmt.Errorf("MCP server entry: %w", err)
	}
	return b, nil
}

func toolName(server string) func(string) string {
	return func(tool string) string { return "mcp__" + server + "__" + tool }
}

// approvalText is the plan-mode flow Claude Code uses to ask for approval of an outline.
const approvalText = "Use plan mode. If this session is not in plan mode, enter it with the `EnterPlanMode` tool. If the user declines to enter plan mode, create nothing: show the outline in a message and say it will be created once they confirm. " +
	"Write the outline as the plan, then call the `ExitPlanMode` tool to ask for approval. If the user rejects it with feedback, revise the outline and ask again. Create or apply the outline only after it is approved.\n\n" +
	"If this session is already in plan mode when this command starts, write tools are unavailable. Do the reading and ask the questions, but create nothing, not even the brief. Write the full plan, including the brief's title and its text word for word, then call the `ExitPlanMode` tool. Reading a ticket is allowed, because it writes nothing; the brief, including its link to the ticket, is created only after the plan is approved. After the plan is approved, create the brief and then the outline."

type commandHead struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint,omitempty"`
}

func command(ins workflow.Instruction, server string) ([]byte, error) {
	body, err := ins.Render(workflow.RenderContext{
		Tool:      toolName(server),
		Arguments: "$ARGUMENTS",
		Text:      map[string]string{"approval": approvalText},
	})
	if err != nil {
		return nil, err
	}
	head, err := yaml.Marshal(commandHead{Description: ins.Description, ArgumentHint: ins.ArgumentHint})
	if err != nil {
		return nil, fmt.Errorf("command %s: front matter: %w", ins.Name, err)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(head)
	buf.WriteString("---\n\n")
	buf.WriteString(body)
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}
