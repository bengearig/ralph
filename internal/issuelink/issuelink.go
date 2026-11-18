// Package issuelink ties a unit to the ticket it was started from.
package issuelink

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/units"
)

const targetPrefix = "issue:"

var targetPattern = regexp.MustCompile(`^([a-z]+):([A-Z][A-Z0-9_]*-[1-9][0-9]*)$`)

// Target returns the link target that names ref: issue:<system>:<KEY>.
func Target(ref issuesource.Ref) string { return targetPrefix + ref.System + ":" + ref.Key }

// ParseTarget returns the ticket a link target names, false when it is not an issue link.
func ParseTarget(target string) (issuesource.Ref, bool) {
	rest, ok := strings.CutPrefix(target, targetPrefix)
	if !ok {
		return issuesource.Ref{}, false
	}
	m := targetPattern.FindStringSubmatch(rest)
	if m == nil {
		return issuesource.Ref{}, false
	}
	return issuesource.Ref{System: m[1], Key: m[2]}, true
}

func own(ctx context.Context, svc units.Service, unitID string) (issuesource.Ref, bool, error) {
	links, err := svc.Links(ctx, unitID)
	if err != nil {
		return issuesource.Ref{}, false, fmt.Errorf("issuelink: links of %s: %w", unitID, err)
	}
	for _, l := range links {
		if l.Kind != units.LinkExternal {
			continue
		}
		if ref, ok := ParseTarget(l.Target); ok {
			return ref, true, nil
		}
	}
	return issuesource.Ref{}, false, nil
}

// Of returns the ticket a unit was started from: its own issue link, else its parent's, else its grandparent's.
func Of(ctx context.Context, svc units.Service, unitID string) (issuesource.Ref, bool, error) {
	id := unitID
	for range 3 {
		if id == "" {
			break
		}
		ref, ok, err := own(ctx, svc, id)
		if err != nil || ok {
			return ref, ok, err
		}
		u, err := svc.Get(ctx, id)
		if err != nil {
			return issuesource.Ref{}, false, fmt.Errorf("issuelink: unit %s: %w", id, err)
		}
		id = u.ParentID
	}
	return issuesource.Ref{}, false, nil
}
