package cli

import (
	"context"
	"os"

	"github.com/bengearig/ralph/internal/identity"
	"github.com/bengearig/ralph/internal/units"
	"github.com/spf13/cobra"
)

// actorContext attributes writes made through the returned context to the git
// identity of the current directory. The identity is resolved on first use and
// kept, so commands that never write never run git.
func (a *app) actorContext(ctx context.Context, _ *cobra.Command) context.Context {
	if a.actor == "" {
		dir, err := os.Getwd()
		if err != nil {
			dir = "."
		}
		a.actor = identity.Resolve(ctx, dir)
	}
	return units.WithActor(ctx, a.actor)
}
