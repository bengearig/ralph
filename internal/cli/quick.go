package cli

import (
	"context"
	"strconv"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

// newQuickService builds the quick work service from the loaded configuration.
func newQuickService(ctx context.Context, a *app, svc units.Service) (*quick.Service, error) {
	if a.root == "" {
		return nil, errNotInRepository
	}
	ws, err := quick.NewLocalWorkspace(a.root)
	if err != nil {
		return nil, err
	}
	settings := verify.FromConfig(a.cfg, a.root)
	trailer, _ := strconv.ParseBool(a.setting("git.commit_trailer"))
	draft, _ := strconv.ParseBool(a.setting("pr.draft"))
	return quick.New(quick.Deps{
		Units:     svc,
		Workspace: ws,
		Config: quick.Config{
			Root:           a.root,
			Remote:         a.setting("pr.remote"),
			Base:           a.setting("pr.base_branch"),
			BranchTemplate: a.setting("git.branch_template"),
			CommitTemplate: a.setting("git.commit_template"),
			CommitTrailer:  trailer,
			Draft:          draft,
			Verify:         settings,
		},
		Provider: func(ctx context.Context) (hosting.Provider, error) { return openProvider(ctx, a) },
		Runner:   verify.Runner{Dir: a.root, Timeout: settings.Timeout},
	}), nil
}
