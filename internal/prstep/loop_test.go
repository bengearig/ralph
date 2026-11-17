package prstep_test

import (
	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/project"
)

// loopPlan returns the branch the loop's preparation gives the slice.
func loopPlan(e *env, cfg *config.Config) (string, error) {
	if _, err := project.EnsureIgnore(e.repo.Dir, project.LocalIgnoreEntries()); err != nil {
		return "", err
	}
	e.repo.Commit("ignore local ralph files")
	e.repo.Git("push", "origin", "main")
	plan, err := loop.Prepare(ctx, loop.Deps{Units: e.units, Workspace: e.deps.Workspace, Config: cfg, Root: e.repo.Dir},
		loop.PrepareInput{Slice: e.slice.ID, DryRun: true})
	return plan.Branch, err
}
