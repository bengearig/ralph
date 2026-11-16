// Package opencode describes ralph's integration with OpenCode.
package opencode

import "github.com/bengearig/ralph/internal/harness"

const name = "opencode"

type adapter struct{}

// New returns the OpenCode adapter.
func New() harness.Adapter { return adapter{} }

func (adapter) Name() string { return name }

func (adapter) Artifacts(harness.Inputs) ([]harness.Artifact, error) {
	return []harness.Artifact{}, nil
}
