// Package quick delivers small pieces of work as an orphan task on its own
// branch: Start checks the workspace, records the task and opens its branch.
// It works over the units service and the workspace seam and knows nothing of
// the command line or MCP.
package quick

import (
	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

// Config holds the settings quick work reads.
type Config struct {
	Root string
	// Remote is pr.remote.
	Remote string
	// Base is pr.base_branch; empty means detect.
	Base           string
	BranchTemplate string
	CommitTemplate string
	CommitTrailer  bool
	Draft          bool
	Verify         verify.Settings
}

// Deps are the collaborators of a Service.
type Deps struct {
	Units     units.Service
	Workspace workspace.Workspace
	Config    Config
}

// Service runs quick work.
type Service struct {
	deps Deps
}

func New(d Deps) *Service { return &Service{deps: d} }

// NewLocalWorkspace opens the checkout at root, leaving out the files ralph
// keeps local.
func NewLocalWorkspace(root string) (workspace.Workspace, error) {
	paths, err := integration.LocalPaths(root)
	if err != nil {
		return nil, err
	}
	return workspace.NewLocal(root, workspace.WithIgnored(paths)), nil
}
