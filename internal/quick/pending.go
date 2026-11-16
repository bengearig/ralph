package quick

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/bengearig/ralph/internal/units"
)

// PendingTask is quick work that was started and not finalized.
type PendingTask struct {
	ID     string
	Title  string
	Branch string
}

// branchLink returns the branch a task's external link names, or "" when it has none.
func branchLink(ctx context.Context, svc units.Service, id string) (string, error) {
	links, err := svc.Links(ctx, id)
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if l.Kind == units.LinkExternal {
			if name, ok := strings.CutPrefix(l.Target, branchLinkPrefix); ok && name != "" {
				return name, nil
			}
		}
	}
	return "", nil
}

// Pending lists the tasks with no parent that are in progress and name a
// branch, in ID order. It reads the work records only.
func Pending(ctx context.Context, svc units.Service) ([]PendingTask, error) {
	tasks, err := svc.List(ctx, units.Filter{Level: units.LevelTask, States: []units.State{units.StateInProgress}})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(tasks, func(a, b units.Unit) int { return a.Number - b.Number })
	out := []PendingTask{}
	for _, t := range tasks {
		if t.ParentID != "" || t.State != units.StateInProgress {
			continue
		}
		branch, err := branchLink(ctx, svc, t.ID)
		if err != nil {
			return nil, err
		}
		if branch == "" {
			continue
		}
		out = append(out, PendingTask{ID: t.ID, Title: t.Title, Branch: branch})
	}
	return out, nil
}

// PendingMessages words each pending task as a notice for an agent to relay.
func PendingMessages(ctx context.Context, svc units.Service) ([]string, error) {
	pending, err := Pending(ctx, svc)
	if err != nil {
		return nil, err
	}
	msgs := make([]string, 0, len(pending))
	for _, p := range pending {
		msgs = append(msgs, fmt.Sprintf(`%s "%s" was started and not finalized (branch %s). Ask the agent to run quick_finish, or cancel the task if the work was dropped.`,
			p.ID, p.Title, p.Branch))
	}
	return msgs, nil
}
