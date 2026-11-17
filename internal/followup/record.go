package followup

import (
	"context"

	"github.com/bengearig/ralph/internal/prstep"
)

// Record writes what the host reported for a polled observation, as
// prstep.Apply does; any other observation is unchanged.
func Record(ctx context.Context, d Deps, o Observation) (prstep.Change, error) {
	if !o.Polled || o.Err != nil {
		return prstep.Change{Kind: prstep.ChangeUnchanged}, nil
	}
	return prstep.Apply(ctx, d.Units, o.Slice, o.Record, o.PullRequest)
}

// CheckSlice reads the latest pull request of one slice from the host and
// records what changed.
func CheckSlice(ctx context.Context, d Deps, sliceID string) (Observation, prstep.Change, error) {
	res, err := Observe(ctx, d, ObserveOptions{Slices: []string{sliceID}, Poll: true, stateOnly: true})
	if err != nil {
		return Observation{}, prstep.Change{}, err
	}
	switch {
	case res.ProviderErr != nil:
		return Observation{}, prstep.Change{}, res.ProviderErr
	case res.RateLimited != nil:
		return Observation{}, prstep.Change{}, res.RateLimited
	}
	o := res.Observations[0]
	if o.Err != nil {
		return o, prstep.Change{}, o.Err
	}
	change, err := Record(ctx, d, o)
	return o, change, err
}
