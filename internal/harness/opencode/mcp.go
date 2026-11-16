package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/bengearig/ralph/internal/harness"
)

var configFiles = []string{"opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc"}

func configFile(project fs.FS) (string, error) {
	if project == nil {
		return configFiles[0], nil
	}
	for _, p := range configFiles {
		info, err := fs.Stat(project, p)
		switch {
		case err == nil:
			if info.Mode().IsRegular() {
				return p, nil
			}
		case errors.Is(err, fs.ErrNotExist):
		default:
			return "", fmt.Errorf("%s: %w", p, err)
		}
	}
	return configFiles[0], nil
}

func mcpArtifact(in harness.Inputs) (harness.Artifact, error) {
	path, err := configFile(in.Project)
	if err != nil {
		return harness.Artifact{}, err
	}
	content, err := json.Marshal(map[string]any{
		"type":    "local",
		"command": append([]string{in.MCP.Command}, in.MCP.Args...),
	})
	if err != nil {
		return harness.Artifact{}, err
	}
	return harness.Artifact{
		Kind:    harness.KindJSONEntry,
		Scope:   harness.ScopeProject,
		Path:    path,
		KeyPath: []string{"mcp", "servers", in.MCP.Name},
		Content: content,
		Label:   "MCP server in " + path,
	}, nil
}
