package opencode

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/workflow"
)

// instructionFile never hides a user's CLAUDE.md: OpenCode reads it only
// when there is no AGENTS.md.
func instructionFile(project fs.FS) (string, error) {
	if project == nil {
		return "AGENTS.md", nil
	}
	for _, p := range []string{"AGENTS.md", "CLAUDE.md"} {
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
	return "AGENTS.md", nil
}

func sectionArtifact(in harness.Inputs) (harness.Artifact, error) {
	path, err := instructionFile(in.Project)
	if err != nil {
		return harness.Artifact{}, err
	}
	notes := in.ProjectNotes
	if notes == "" {
		notes = workflow.ProjectNotes(in.Instructions)
	}
	return harness.Artifact{
		Kind:    harness.KindSection,
		Scope:   harness.ScopeProject,
		Path:    path,
		Content: []byte(notes),
		Label:   "section in " + path,
	}, nil
}
