// Package opencode describes ralph's integration with OpenCode.
package opencode

import "github.com/bengearig/ralph/internal/harness"

const name = "opencode"

type adapter struct{}

// New returns the OpenCode adapter.
func New() harness.Adapter { return adapter{} }

func (adapter) Name() string { return name }

func (adapter) Artifacts(in harness.Inputs) ([]harness.Artifact, error) {
	arts := make([]harness.Artifact, 0, len(in.Instructions)+1)
	for _, ins := range in.Instructions {
		a, err := commandArtifact(ins, in.MCP.Name)
		if err != nil {
			return nil, err
		}
		arts = append(arts, a)
	}
	sec, err := sectionArtifact(in)
	if err != nil {
		return nil, err
	}
	return append(arts, sec), nil
}
