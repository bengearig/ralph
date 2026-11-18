package prstep

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/units"
)

const branchLinkPrefix = "branch:"

// sliceData is the naming data of a slice, the same the loop renders its branch with.
func sliceData(slice units.Unit, ticket string) naming.Data {
	return naming.Data{
		ID: slice.ID, Level: "slice", Number: slice.Number, Title: slice.Title,
		Slug: naming.Slugify(slice.Title), ParentID: slice.ParentID, Ticket: ticket,
	}
}

// BranchName renders the branch of a slice that has no ticket from the branch template.
func BranchName(template string, slice units.Unit) (string, error) {
	name, err := naming.RenderBranch(template, sliceData(slice, ""))
	if err != nil {
		return "", fmt.Errorf("render the branch name: %w", err)
	}
	return name, nil
}

// SliceTicket returns the key of the ticket a slice, or its brief, was started from; empty when none.
func SliceTicket(ctx context.Context, svc units.Service, slice units.Unit) (string, error) {
	ref, ok, err := issuelink.Of(ctx, svc, slice.ID)
	if err != nil || !ok {
		return "", err
	}
	return ref.Key, nil
}

// RecordedBranch returns the branch a unit's branch link names, empty when it has none.
func RecordedBranch(ctx context.Context, svc units.Service, id string) (string, error) {
	links, err := svc.Links(ctx, id)
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if l.Kind != units.LinkExternal {
			continue
		}
		if name, ok := strings.CutPrefix(l.Target, branchLinkPrefix); ok && name != "" {
			return name, nil
		}
	}
	return "", nil
}

// RecordBranch records the branch of a slice so that later changes to its ticket never rename it.
func RecordBranch(ctx context.Context, svc units.Service, sliceID, branch string) error {
	return svc.AddLink(ctx, sliceID, units.LinkExternal, branchLinkPrefix+branch)
}

// SliceBranch returns the branch of a slice: the recorded one when there is one, else the template
// rendered with the slice's ticket.
func SliceBranch(ctx context.Context, svc units.Service, template string, slice units.Unit) (string, error) {
	recorded, err := RecordedBranch(ctx, svc, slice.ID)
	if err != nil {
		return "", fmt.Errorf("read the branch link of %s: %w", slice.ID, err)
	}
	if recorded != "" {
		return recorded, nil
	}
	ticket, err := SliceTicket(ctx, svc, slice)
	if err != nil {
		return "", err
	}
	name, err := naming.RenderBranch(template, sliceData(slice, ticket))
	if err != nil {
		return "", fmt.Errorf("render the branch name: %w", err)
	}
	return name, nil
}
