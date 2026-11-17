package followup

import (
	"fmt"
	"sort"

	"github.com/bengearig/ralph/internal/units"
)

// PlanOptions are the settings Plan reads.
type PlanOptions struct {
	// DeleteBranches is local, both or none; empty means local.
	DeleteBranches string
}

// Plan lists what to do about the observations, by slice number and then kind.
// It changes nothing.
func Plan(obs []Observation, opt PlanOptions) []Followup {
	sorted := append([]Observation(nil), obs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Slice.Number < sorted[j].Slice.Number })
	var out []Followup
	for _, o := range sorted {
		f := planOne(o, opt)
		sort.SliceStable(f, func(i, j int) bool { return f[i].Kind.Order() < f[j].Kind.Order() })
		out = append(out, f...)
	}
	return out
}

func planOne(o Observation, opt PlanOptions) []Followup {
	rec := o.Record
	state := rec.State
	polled := o.Polled && o.Err == nil
	if polled {
		state = units.PullRequestState(o.PullRequest.State)
	}
	add := func(kind Kind, summary string) Followup {
		return Followup{Kind: kind, Slice: o.Slice.ID, Summary: summary, Number: rec.Number}
	}
	var out []Followup
	if polled && state != rec.State {
		out = append(out, add(KindRecord, fmt.Sprintf("pull request #%d: %s on the host (recorded: %s)", rec.Number, state, rec.State)))
	}
	if state == units.PullRequestClosed && rec.Resolution == "" {
		out = append(out, add(KindDecision, fmt.Sprintf("pull request #%d was closed without merging; choose ralph pr reopen, rearticulate or cancel %s", rec.Number, o.Slice.ID)))
	}
	if state == units.PullRequestOpen && len(o.NewFeedback) > 0 {
		noun := "items"
		if len(o.NewFeedback) == 1 {
			noun = "item"
		}
		out = append(out, add(KindFeedback, fmt.Sprintf("%d new review feedback %s", len(o.NewFeedback), noun)))
	}
	if state == units.PullRequestMerged && opt.DeleteBranches != DeleteNone &&
		(o.LocalBranchExists || (opt.DeleteBranches == DeleteBoth && o.RemoteBranchExists)) {
		out = append(out, add(KindClose, "delete branch "+o.LocalBranch))
	}
	return out
}
