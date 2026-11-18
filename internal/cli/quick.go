package cli

import (
	"context"
	"strconv"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

// newQuickService builds the quick work service from the loaded configuration.
func newQuickService(ctx context.Context, a *app, svc units.Service, d *db.DB) (*quick.Service, error) {
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
	deps := quick.Deps{
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
	}
	issues, err := quickIssues(ctx, a)
	if err != nil {
		return nil, err
	}
	if issues != nil {
		tm, err := taskmanager.FromConfig(a.cfg)
		if err != nil {
			return nil, err
		}
		deps.Issues = issues
		deps.Tickets = tickets.Deps{DB: d, Settings: tm, ProjectID: a.setting("project.id")}
	}
	return quick.New(deps), nil
}

// quickIssues returns the issue source, nil when none is configured. A source that is configured but cannot be
// opened is returned as one that reports why, so only a start from a ticket fails.
func quickIssues(ctx context.Context, a *app) (issuesource.Source, error) {
	settings, err := issuesource.FromConfig(a.cfg)
	if err != nil {
		return nil, err
	}
	if settings.Provider != "jira" {
		return nil, nil
	}
	src, err := openIssueSource(ctx, a)
	if err != nil {
		return brokenSource{err}, nil
	}
	return src, nil
}

// brokenSource is an issue source that could not be opened.
type brokenSource struct{ err error }

func (b brokenSource) Name() string { return "jira" }

func (b brokenSource) Parse(string) (issuesource.Ref, error) { return issuesource.Ref{}, b.err }

func (b brokenSource) Get(context.Context, issuesource.Ref) (issuesource.Issue, error) {
	return issuesource.Issue{}, b.err
}
