package opencode

import (
	"bytes"
	"fmt"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/workflow"
	"go.yaml.in/yaml/v3"
)

type commandHead struct {
	Description string `yaml:"description"`
}

func commandArtifact(ins workflow.Instruction, server string) (harness.Artifact, error) {
	content, err := command(ins, server)
	if err != nil {
		return harness.Artifact{}, err
	}
	return harness.Artifact{
		Kind:    harness.KindFile,
		Scope:   harness.ScopeProject,
		Path:    ".opencode/commands/ralph/" + ins.Name + ".md",
		Content: content,
		Label:   "command /ralph/" + ins.Name,
	}, nil
}

func command(ins workflow.Instruction, server string) ([]byte, error) {
	body, err := ins.Render(workflow.RenderContext{
		Tool:      func(tool string) string { return tool },
		Arguments: "$ARGUMENTS",
	})
	if err != nil {
		return nil, err
	}
	desc := ins.Description
	if ins.ArgumentHint != "" {
		desc += " " + ins.ArgumentHint
	}
	head, err := yaml.Marshal(commandHead{Description: desc})
	if err != nil {
		return nil, fmt.Errorf("command %s: front matter: %w", ins.Name, err)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(head)
	buf.WriteString("---\n\n")
	fmt.Fprintf(&buf, "The tools named below are provided by the MCP server %q.\n\n", server)
	buf.WriteString(body)
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}
