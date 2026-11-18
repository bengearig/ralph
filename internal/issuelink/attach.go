package issuelink

import (
	"context"
	"errors"
	"fmt"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
)

// ErrAlreadyLinked means a unit already holds the ticket.
var ErrAlreadyLinked = errors.New("ticket is already linked")

type alreadyLinked struct{ key, unit string }

func (e alreadyLinked) Error() string {
	return fmt.Sprintf("%s is already linked to %s", e.key, e.unit)
}
func (e alreadyLinked) Is(target error) bool { return target == ErrAlreadyLinked }

// Check says whether a unit may be started from a ticket. It writes nothing. An empty unitID checks the ticket
// alone, for a unit that does not exist yet.
func Check(ctx context.Context, d tickets.Deps, unitID string, ref issuesource.Ref) error {
	return check(ctx, d, unitID, ref, false)
}

func check(ctx context.Context, d tickets.Deps, unitID string, ref issuesource.Ref, repair bool) error {
	row, ok, err := tickets.NewStore(d.DB).ByKey(ctx, ref.System, ref.Key)
	if err != nil {
		return err
	}
	if ok {
		return alreadyLinked{key: ref.Key, unit: row.UnitID}
	}
	if unitID == "" {
		return nil
	}
	if _, err := d.Units.Get(ctx, unitID); err != nil {
		return fmt.Errorf("issuelink: unit %s: %w", unitID, err)
	}
	have, ok, err := own(ctx, d.Units, unitID)
	if err != nil {
		return err
	}
	if ok && (!repair || have != ref) {
		return fmt.Errorf("%s was already started from %s", unitID, have.Key)
	}
	return nil
}

// Attach links a unit to the ticket it was started from and records the ticket's row, so that no second ticket
// is made for it. The links are written before the row; a second call for the same unit and ticket completes
// what a failure left undone.
func Attach(ctx context.Context, d tickets.Deps, unitID string, issue issuesource.Issue) error {
	ref := issue.Ref
	if err := check(ctx, d, unitID, ref, true); err != nil {
		return err
	}
	for _, target := range []string{Target(ref), ref.String()} {
		if err := d.Units.AddLink(ctx, unitID, units.LinkExternal, target); err != nil {
			return fmt.Errorf("issuelink: link %s to %s: %w", unitID, target, err)
		}
	}
	states, err := d.Units.Statuses(ctx, []string{unitID})
	if err != nil {
		return fmt.Errorf("issuelink: status of %s: %w", unitID, err)
	}
	status, _ := tickets.StatusFor(d.Settings, string(states[unitID]))
	row := tickets.Row{
		UnitID: unitID, System: ref.System, Key: ref.Key, PushedStatus: status,
		Scheduled: true, LinkConfirmed: true, Origin: tickets.OriginIssue,
	}
	return tickets.NewStore(d.DB).Put(ctx, row)
}
