package followup

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
)

// ApplyOptions say which slices to handle and which kinds of follow-up to
// apply; a record is always applied.
type ApplyOptions struct {
	Slices []string
	Kinds  []Kind
}

// ApplyResult reports a run of Apply. NothingObserved is set when the provider
// could not be used or no slice that needed the host could be read from it.
type ApplyResult struct {
	Lines           []string `json:"lines"`
	Recorded        int      `json:"recorded"`
	Created         int      `json:"created"`
	Closed          int      `json:"closed"`
	NotApplied      int      `json:"not_applied"`
	Failures        []string `json:"failures"`
	NothingObserved bool     `json:"nothing_observed"`
}

var kindFlags = map[Kind]string{KindClose: "--close", KindFeedback: "--feedback"}

// Apply observes the slices, records what the host reports, and applies the
// follow-ups of the selected kinds. A failure of one applier is reported and
// does not stop the other slices.
func Apply(ctx context.Context, d Deps, opt ApplyOptions) (ApplyResult, error) {
	res := ApplyResult{Lines: []string{}, Failures: []string{}}
	obs, err := Observe(ctx, d, ObserveOptions{Slices: opt.Slices, Poll: true})
	if err != nil {
		return res, err
	}
	wanted := 0
	polled := 0
	for _, o := range obs.Observations {
		if !wantsPoll(o.Record, ObserveOptions{Poll: true}) {
			continue
		}
		wanted++
		if o.Polled && o.Err == nil {
			polled++
		}
	}
	res.NothingObserved = obs.ProviderErr != nil || (wanted > 0 && polled == 0)
	popt := PlanOptions{DeleteBranches: d.DeleteBranches}
	fail := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		res.Lines = append(res.Lines, line)
		res.Failures = append(res.Failures, line)
	}
	kinds := append([]Kind(nil), opt.Kinds...)
	sort.SliceStable(kinds, func(i, j int) bool { return kinds[i].Order() < kinds[j].Order() })

	for _, o := range obs.Observations {
		id := o.Slice.ID
		if err := ctx.Err(); err != nil {
			return res, err
		}
		switch {
		case obs.ProviderErr != nil && wantsPoll(o.Record, ObserveOptions{Poll: true}):
			fail("%s: not observed: provider unavailable (%v)", id, obs.ProviderErr)
		case o.Err != nil:
			fail("%s: %v", id, o.Err)
			continue
		case obs.RateLimited != nil && wantsPoll(o.Record, ObserveOptions{Poll: true}) && !o.Polled:
			fail("%s: not observed: rate limited (%v)", id, obs.RateLimited)
		}

		if hasKind(Plan([]Observation{o}, popt), KindRecord) {
			change, err := Record(ctx, d, o)
			if err != nil {
				fail("%s: record failed: %v", id, err)
				continue
			}
			res.Recorded++
			res.Lines = append(res.Lines, fmt.Sprintf("recorded %s: %s", id, change.Message))
			o.Record.State = units.PullRequestState(o.PullRequest.State)
		}

		for _, f := range Plan([]Observation{o}, popt) {
			switch {
			case f.Kind == KindRecord:
			case f.Kind == KindDecision:
				res.Lines = append(res.Lines, "needs you: "+f.Summary)
			case !slices.Contains(kinds, f.Kind):
				if flag := kindFlags[f.Kind]; flag != "" {
					res.NotApplied++
					res.Lines = append(res.Lines, fmt.Sprintf("not applied: %s: %s: %s; apply it with %s", f.Kind, id, f.Summary, flag))
				}
			case f.Kind == KindFeedback:
				applyFeedback(ctx, d, id, &res, fail)
			case f.Kind == KindClose:
				applyClose(ctx, d, o, &res, fail)
			}
		}
	}
	return res, nil
}

func hasKind(plan []Followup, k Kind) bool {
	return slices.ContainsFunc(plan, func(f Followup) bool { return f.Kind == k })
}

func applyFeedback(ctx context.Context, d Deps, id string, res *ApplyResult, fail func(string, ...any)) {
	pd := d.PR
	pd.Units = d.Units
	rep, err := prstep.Feedback(ctx, pd, id, prstep.FeedbackOptions{Create: true})
	var created []string
	for _, it := range rep.Items {
		if it.Created != "" {
			created = append(created, it.Created)
		}
	}
	res.Created += len(created)
	if len(created) > 0 {
		res.Lines = append(res.Lines, fmt.Sprintf("created %s from review feedback on %s", strings.Join(created, ", "), id))
	} else if err == nil {
		res.Lines = append(res.Lines, fmt.Sprintf("no new tasks from review feedback on %s", id))
	}
	if err != nil {
		fail("%s: review feedback failed: %v", id, err)
	}
}

// applyClose gets the pull request from the host when the observation did not
// poll it, because the head commit decides what may be deleted.
func applyClose(ctx context.Context, d Deps, o Observation, res *ApplyResult, fail func(string, ...any)) {
	id := o.Slice.ID
	if !o.Polled {
		p, err := buildProvider(ctx, d)
		if err == nil {
			var pr hosting.PullRequest
			if pr, err = p.GetPullRequest(ctx, o.Record.Number); err == nil {
				o.Polled, o.PullRequest = true, pr
			}
		}
		if err != nil {
			fail("%s: close failed: read pull request #%d: %v", id, o.Record.Number, err)
			return
		}
	}
	cr, err := Close(ctx, d, o)
	for _, l := range cr.Lines {
		res.Lines = append(res.Lines, id+": "+l)
	}
	if len(cr.Deleted) > 0 {
		res.Closed++
	}
	if err != nil {
		fail("%s: close failed: %v", id, err)
	}
}
