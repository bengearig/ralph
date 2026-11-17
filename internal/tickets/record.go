package tickets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/units"
)

const stateLastPoll = "last_poll"

// RecordResult counts what Record wrote: rows with a difference, rows whose difference went away and tickets
// followed to a new key.
type RecordResult struct {
	Found, Cleared, Followed int
}

// CheckResult is what Check found and recorded.
type CheckResult struct {
	Drifts []Drift
	Result RecordResult
}

type linkChange struct{ unit, from, to string }

// Record writes what was found: the drift of each row, the new key of each followed ticket, and the time of the
// poll. The rows change in one transaction; the links of followed tickets change just before it and are put back
// if it does not commit.
func Record(ctx context.Context, d Deps, obs []Observation, drifts []Drift) (res RecordResult, err error) {
	byUnit := make(map[string]Drift, len(drifts))
	for _, dr := range drifts {
		byUnit[dr.Unit] = dr
	}
	now := d.now()
	var followed []linkChange
	for _, dr := range drifts {
		if dr.MovedTo != "" {
			followed = append(followed, linkChange{dr.Unit, linkPrefix + dr.Key, linkPrefix + dr.MovedTo})
		}
	}
	done, err := changeLinks(ctx, d.Units, followed)
	if err != nil {
		return RecordResult{}, err
	}
	defer func() {
		if err != nil {
			res = RecordResult{}
			err = joinErr(err, revertLinks(ctx, d.Units, done))
		}
	}()

	err = d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res = RecordResult{}
		for _, o := range obs {
			dr, has := byUnit[o.Unit]
			if has && len(dr.Fields) > 0 {
				res.Found++
				_, err := tx.ExecContext(ctx, "UPDATE tickets SET drift = ?, drift_seen_at = ? WHERE unit_id = ?",
					strings.Join(dr.Fields, ","), formatTime(now), o.Unit)
				if err != nil {
					return fmt.Errorf("tickets: record drift of %s: %w", o.Unit, err)
				}
			} else if o.Row.Drift != "" && (o.Row.Drift != driftDeleted || o.Ticket != nil) {
				res.Cleared++
				if _, err := tx.ExecContext(ctx, "UPDATE tickets SET drift = '' WHERE unit_id = ?", o.Unit); err != nil {
					return fmt.Errorf("tickets: clear drift of %s: %w", o.Unit, err)
				}
			}
			if has && dr.MovedTo != "" {
				res.Followed++
				if _, err := tx.ExecContext(ctx, "UPDATE tickets SET ticket_key = ? WHERE unit_id = ?", dr.MovedTo, o.Unit); err != nil {
					return fmt.Errorf("tickets: follow %s to %s: %w", o.Unit, dr.MovedTo, err)
				}
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO ticket_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
			stateLastPoll, now.UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("tickets: write state %s: %w", stateLastPoll, err)
		}
		return nil
	})
	return res, err
}

// changeLinks swaps each unit's old link for the new one and returns the swaps made, also when it stops with an error.
func changeLinks(ctx context.Context, svc units.Service, changes []linkChange) ([]linkChange, error) {
	var done []linkChange
	for _, c := range changes {
		if err := svc.AddLink(ctx, c.unit, units.LinkExternal, c.to); err != nil {
			return done, joinErr(fmt.Errorf("tickets: link %s: %w", c.unit, err), revertLinks(ctx, svc, done))
		}
		if err := svc.RemoveLink(ctx, c.unit, units.LinkExternal, c.from); err != nil {
			_ = svc.RemoveLink(ctx, c.unit, units.LinkExternal, c.to)
			return done, joinErr(fmt.Errorf("tickets: unlink %s: %w", c.unit, err), revertLinks(ctx, svc, done))
		}
		done = append(done, c)
	}
	return done, nil
}

func revertLinks(ctx context.Context, svc units.Service, done []linkChange) error {
	var err error
	for i := len(done) - 1; i >= 0; i-- {
		c := done[i]
		err = joinErr(err, svc.AddLink(ctx, c.unit, units.LinkExternal, c.from))
		err = joinErr(err, svc.RemoveLink(ctx, c.unit, units.LinkExternal, c.to))
	}
	return err
}

func joinErr(a, b error) error {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return fmt.Errorf("%w; %w", a, b)
}

// Check observes the tickets, works out their drifts and records them.
func Check(ctx context.Context, d Deps, opt ObserveOptions) (CheckResult, error) {
	seen, err := Observe(ctx, d, opt)
	if err != nil {
		return CheckResult{}, err
	}
	drifts := Plan(seen.Observations, d.System.Normalize)
	res, err := Record(ctx, d, seen.Observations, drifts)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{Drifts: drifts, Result: res}, nil
}
