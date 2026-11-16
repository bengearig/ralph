// Package harness defines the adapter interface that describes how ralph
// integrates with one agent harness. An adapter must not write files or run the
// harness, other than to read its version in Detect; it describes artifacts as
// data and the installer applies them.
package harness

import (
	"context"

	"github.com/bengearig/ralph/internal/workflow"
)

// Inputs is what every adapter renders its artifacts from.
type Inputs struct {
	Instructions []workflow.Instruction
	ProjectNotes string
	MCP          MCPServer
}

// MCPServer describes how a harness starts ralph's MCP server.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
}

// Detection reports whether a harness is present.
type Detection struct {
	Found   bool
	Version string
	Detail  string
}

// Adapter describes the integration with one harness.
type Adapter interface {
	// Name is the lowercase identifier used on the command line and in manifests.
	Name() string
	// Detect reports whether the harness is available for the project at root.
	Detect(ctx context.Context, root string) Detection
	// Artifacts returns, deterministically, what should exist for the inputs.
	Artifacts(in Inputs) ([]Artifact, error)
}
