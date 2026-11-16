// Package quick delivers small pieces of work as an orphan task on its own
// branch: Start checks the workspace, records the task and opens its branch.
// It works over the units service and the workspace seam and knows nothing of
// the command line or MCP.
package quick

import (
	"context"

	"github.com/bengearig/ralph/internal/hosting"
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
	// Provider is built when a pull request is needed, so a missing token only matters then.
	Provider func(ctx context.Context) (hosting.Provider, error)
	// Runner runs the verification commands; nil means a verify.Runner over the project root.
	Runner Runner
}

// Runner runs verification commands; verify.Runner satisfies it.
type Runner interface {
	Run(ctx context.Context, commands []string) (verify.Report, error)
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
