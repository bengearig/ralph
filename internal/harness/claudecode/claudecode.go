// Package claudecode describes ralph's integration with Claude Code.
package claudecode

import (
	"bytes"
	"context"
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

func (adapter) Detect(context.Context, string) harness.Detection { return harness.Detection{} }

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
		notes = workflow.ProjectNotes(in.Instructions, toolName(in.MCP.Name))
	}
	arts = append(arts, harness.Artifact{
		Kind:    harness.KindSection,
		Scope:   harness.ScopeProject,
		Path:    "CLAUDE.md",
		Content: []byte(notes),
		Label:   "section in CLAUDE.md",
	})
	return arts, nil
}

func toolName(server string) func(string) string {
	return func(tool string) string { return "mcp__" + server + "__" + tool }
}

type commandHead struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint,omitempty"`
}

func command(ins workflow.Instruction, server string) ([]byte, error) {
	body, err := ins.Render(workflow.RenderContext{
		Tool:      toolName(server),
		Arguments: "$ARGUMENTS",
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
