package followup

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
)

// Deps are the services and settings the handler needs.
type Deps struct {
	PR        prstep.Deps
	Units     units.Service
	Workspace workspace.Workspace
	Provider  func(context.Context) (hosting.Provider, error)
	// DeleteBranches is local, both or none; empty means local.
	DeleteBranches string
	// Loop runs the loop for a continue follow-up; nil means none can run.
	Loop LoopRunner
}

// ObserveOptions says which slices to observe and whether to ask the host.
type ObserveOptions struct {
	// Slices are the slice IDs to observe; empty means every slice that has a
	// recorded pull request.
	Slices []string
	Poll   bool
	// stateOnly polls every record, merged and resolved included, for the
	// pull request alone.
	stateOnly bool
}

// Observation is what was seen of one slice.
type Observation struct {
	Slice       units.Unit
	Record      units.PullRequestRecord
	Polled      bool
	PullRequest hosting.PullRequest
	// NewFeedback holds the eligible feedback items that have no task yet.
	NewFeedback       []prstep.FeedbackItem
	LocalBranch       string
	LocalBranchExists bool
	// RemoteBranchExists is set only for a merged slice with DeleteBranches both.
	RemoteBranchExists bool
	// NextSlice is the slice a continue follow-up would start; set only for a
	// merged slice that has one.
	NextSlice string
	Err       error
}

// ObserveResult is what Observe saw. A provider that could not be built and a
// rate limit are reported here and are not errors of Observe.
type ObserveResult struct {
	Observations []Observation
	ProviderErr  error
	RateLimited  *hosting.RateLimitError
}

// Observe reads the recorded and, with Poll, the hosted state of slices'
// latest pull requests. It writes nothing.
func Observe(ctx context.Context, d Deps, opt ObserveOptions) (ObserveResult, error) {
	var res ObserveResult
	slices, err := selectSlices(ctx, d, opt.Slices)
	if err != nil {
		return res, err
	}
	var provider hosting.Provider
	if opt.Poll {
		provider, res.ProviderErr = buildProvider(ctx, d)
	}
	pd := d.PR
	pd.Units = d.Units
	if provider != nil {
		pd.Provider = func(context.Context) (hosting.Provider, error) { return provider, nil }
	}
	limited := false
	for _, s := range slices {
		o := Observation{Slice: s.slice, Record: s.record}
		if provider != nil && !limited && wantsPoll(o.Record, opt) {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			var rl *hosting.RateLimitError
			poll(ctx, pd, provider, &o, opt.stateOnly)
			if errors.As(o.Err, &rl) {
				res.RateLimited = rl
				limited = true
				o.Err = nil
				o.Polled = false
			}
		}
		o.LocalBranch, o.LocalBranchExists, err = localBranch(ctx, d, s.slice)
		if err != nil && o.Err == nil {
			o.Err = err
		}
		if d.DeleteBranches == DeleteBoth && d.Workspace != nil && o.LocalBranch != "" && effectiveState(o) == units.PullRequestMerged {
			_, o.RemoteBranchExists, err = d.Workspace.RemoteBranchTip(ctx, d.PR.Config.Remote, o.LocalBranch)
			if err != nil && o.Err == nil {
				o.Err = fmt.Errorf("look for branch %s on %s: %w", o.LocalBranch, d.PR.Config.Remote, err)
			}
		}
		if effectiveState(o) == units.PullRequestMerged {
			if o.NextSlice, err = NextSlice(ctx, d.Units, s.slice); err != nil && o.Err == nil {
				o.Err = err
			}
		}
		res.Observations = append(res.Observations, o)
	}
	return res, nil
}

func effectiveState(o Observation) units.PullRequestState {
	if o.Polled && o.Err == nil {
		return units.PullRequestState(o.PullRequest.State)
	}
	return o.Record.State
}

type selected struct {
	slice  units.Unit
	record units.PullRequestRecord
}

func selectSlices(ctx context.Context, d Deps, ids []string) ([]selected, error) {
	var units_ []units.Unit
	if len(ids) == 0 {
		all, err := d.Units.List(ctx, units.Filter{Level: units.LevelSlice})
		if err != nil {
			return nil, err
		}
		units_ = all
	} else {
		seen := map[string]bool{}
		for _, id := range ids {
			u, err := d.Units.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if u.Level != units.LevelSlice {
				return nil, fmt.Errorf("%s is a %s, not a slice: give the ID of a slice", u.ID, u.Level)
			}
			if !seen[u.ID] {
				seen[u.ID] = true
				units_ = append(units_, u)
			}
		}
	}
	sort.SliceStable(units_, func(i, j int) bool { return units_[i].Number < units_[j].Number })
	var out []selected
	for _, u := range units_ {
		records, err := d.Units.PullRequests(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			if len(ids) > 0 {
				return nil, fmt.Errorf("%s has no pull request: run ralph pr create %s", u.ID, u.ID)
			}
			continue
		}
		out = append(out, selected{slice: u, record: latest(records)})
	}
	return out, nil
}

func latest(records []units.PullRequestRecord) units.PullRequestRecord {
	best := records[0]
	for _, r := range records[1:] {
		if r.CreatedAt.After(best.CreatedAt) || (r.CreatedAt.Equal(best.CreatedAt) && r.Number > best.Number) {
			best = r
		}
	}
	return best
}

func buildProvider(ctx context.Context, d Deps) (hosting.Provider, error) {
	if d.Provider == nil {
		return nil, errors.New("no pull request provider is configured")
	}
	p, err := d.Provider(ctx)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("no pull request provider is configured")
	}
	return p, nil
}

func wantsPoll(rec units.PullRequestRecord, opt ObserveOptions) bool {
	if !opt.Poll {
		return false
	}
	if opt.stateOnly {
		return true
	}
	return rec.State == units.PullRequestOpen ||
		(rec.State == units.PullRequestClosed && rec.Resolution == "")
}

// poll fills o from the host.
func poll(ctx context.Context, pd prstep.Deps, p hosting.Provider, o *Observation, stateOnly bool) {
	number := o.Record.Number
	if o.Record.State == units.PullRequestOpen && !stateOnly {
		rep, err := prstep.Feedback(ctx, pd, o.Slice.ID, prstep.FeedbackOptions{})
		if err != nil {
			o.Err = err
			return
		}
		o.Polled = true
		o.PullRequest = rep.PullRequest
		for _, item := range rep.Items {
			if item.Eligible && item.Handled == "" {
				o.NewFeedback = append(o.NewFeedback, item)
			}
		}
		return
	}
	pr, err := p.GetPullRequest(ctx, number)
	if errors.Is(err, hosting.ErrNotFound) {
		o.Err = fmt.Errorf("pull request #%d of %s is gone from the host (%s): %w", number, o.Slice.ID, o.Record.URL, err)
		return
	}
	if err != nil {
		o.Err = err
		return
	}
	o.Polled = true
	o.PullRequest = pr
}

func localBranch(ctx context.Context, d Deps, slice units.Unit) (string, bool, error) {
	name, err := prstep.SliceBranch(ctx, d.Units, d.PR.Config.BranchTemplate, slice)
	if err != nil {
		return "", false, err
	}
	if d.Workspace == nil {
		return name, false, nil
	}
	exists, err := d.Workspace.BranchExists(ctx, name)
	if err != nil {
		return name, false, fmt.Errorf("look for branch %s: %w", name, err)
	}
	return name, exists, nil
}
