package prstep

import (
	"fmt"

	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/units"
)

// sliceData is the naming data of a slice, the same the loop renders its branch with.
func sliceData(slice units.Unit) naming.Data {
	return naming.Data{
		ID: slice.ID, Level: "slice", Number: slice.Number, Title: slice.Title,
		Slug: naming.Slugify(slice.Title), ParentID: slice.ParentID,
	}
}

// BranchName renders the branch of a slice from the branch template, as the loop does.
func BranchName(template string, slice units.Unit) (string, error) {
	name, err := naming.RenderBranch(template, sliceData(slice))
	if err != nil {
		return "", fmt.Errorf("render the branch name: %w", err)
	}
	return name, nil
}
