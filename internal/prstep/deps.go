// Package prstep opens a finished slice's pull request. Nothing is pushed or
// opened except by the command that calls it.
package prstep

import (
	"context"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

// Verifier runs the verification commands; verify.Runner satisfies it.
type Verifier interface {
	Run(ctx context.Context, commands []string) (verify.Report, error)
}

// Config holds the settings the step reads.
type Config struct {
	Root           string
	Remote         string
	Base           string
	BranchTemplate string
	CommitTemplate string
	Draft          bool
	Verify         verify.Settings
}

// Deps are the services and settings the step needs.
type Deps struct {
	Units     units.Service
	Workspace workspace.Workspace
	Provider  func(context.Context) (hosting.Provider, error)
	// Verifier is a verify.Runner on the project root when nil.
	Verifier Verifier
	Config   Config
	// Report receives progress lines; nothing is printed when nil.
	Report func(string)
}

func (d Deps) report(line string) {
	if d.Report != nil {
		d.Report(line)
	}
}

func (d Deps) verifier() Verifier {
	if d.Verifier != nil {
		return d.Verifier
	}
	return verify.Runner{Dir: d.Config.Root, Timeout: d.Config.Verify.Timeout}
}
