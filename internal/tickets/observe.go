package tickets

import (
	"context"
	"fmt"
	"slices"

	"github.com/bengearig/ralph/internal/taskmanager"
)

// ObserveOptions say which tickets to observe.
type ObserveOptions struct {
	// Units limits the observation to these units; empty means every row that is not detached.
	Units []string
}

// Observation is what was seen of one unit's ticket. Ticket is nil when the system no longer has it.
type Observation struct {
	Unit   string
	Row    Row
	Ticket *taskmanager.Ticket
}

// ObserveResult is what Observe saw.
type ObserveResult struct {
	Observations []Observation
}

// Observe reads the rows and the tickets they name. It writes nothing, in ralph or in the system.
func Observe(ctx context.Context, d Deps, opt ObserveOptions) (ObserveResult, error) {
	rows, err := observedRows(ctx, d, opt.Units)
	if err != nil {
		return ObserveResult{}, err
	}
	if len(rows) == 0 {
		return ObserveResult{}, nil
	}
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}
	found, err := d.System.Get(ctx, keys)
	if err != nil {
		return ObserveResult{}, fmt.Errorf("tickets: read tickets: %w", err)
	}
	res := ObserveResult{Observations: make([]Observation, 0, len(rows))}
	for _, r := range rows {
		o := Observation{Unit: r.UnitID, Row: r}
		if t, ok := found[r.Key]; ok {
			o.Ticket = &t
		}
		res.Observations = append(res.Observations, o)
	}
	return res, nil
}

func observedRows(ctx context.Context, d Deps, ids []string) ([]Row, error) {
	for _, id := range ids {
		if _, err := d.Units.Get(ctx, id); err != nil {
			return nil, err
		}
	}
	all, err := NewStore(d.DB).List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Row, len(all))
	for _, r := range all {
		byID[r.UnitID] = r
	}
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("tickets: %s has no ticket", id)
		}
	}
	var rows []Row
	for _, r := range all {
		if len(ids) > 0 && !slices.Contains(ids, r.UnitID) {
			continue
		}
		links, err := d.Units.Links(ctx, r.UnitID)
		if err != nil {
			return nil, fmt.Errorf("tickets: links of %s: %w", r.UnitID, err)
		}
		if r.LinkConfirmed && LinkKey(links) == "" {
			if len(ids) > 0 {
				return nil, fmt.Errorf("tickets: %s is detached from its ticket", r.UnitID)
			}
			continue
		}
		rows = append(rows, r)
	}
	return rows, nil
}
