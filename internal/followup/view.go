package followup

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bengearig/ralph/internal/units"
)

// ViewOptions say what BuildView shows.
type ViewOptions struct {
	// ID restricts the view to one brief or slice.
	ID string
	// All includes finished briefs and slices.
	All bool
	// Poll asks the host about open pull requests.
	Poll bool
	// Now gives the time of the view; nil means the clock.
	Now func() time.Time
}

// View is what the monitor shows.
type View struct {
	At        time.Time   `json:"at"`
	Provider  string      `json:"provider"`
	Briefs    []BriefView `json:"briefs"`
	Followups []Followup  `json:"followups"`
}

// BriefView is a brief and its slices.
type BriefView struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Status string      `json:"status"`
	Slices []SliceView `json:"slices"`
}

// SliceView is a slice with what is known of its latest pull request.
type SliceView struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Status      string           `json:"status"`
	WaitingOn   []string         `json:"waiting_on,omitempty"`
	PullRequest *PullRequestView `json:"pull_request,omitempty"`
	NewFeedback int              `json:"new_feedback"`
	Followups   []Followup       `json:"followups"`
	Error       string           `json:"error,omitempty"`
}

// PullRequestView is the recorded and, when polled, the hosted state of a
// pull request. Host is empty when it was not polled.
type PullRequestView struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Recorded   string `json:"recorded"`
	Host       string `json:"host,omitempty"`
	Draft      bool   `json:"draft"`
	Resolution string `json:"resolution,omitempty"`
}

// Provider values of View.
const (
	ProviderOK        = "ok"
	ProviderNotPolled = "not polled"
)

// A merged slice has this derived status once the host reports the merge.
const statusMerged units.Status = "MERGED"

func statusFinished(s units.Status) bool {
	return s == units.StatusDone || s == units.StatusCancelled || s == statusMerged
}

type briefScope struct {
	brief  units.Unit
	slices []units.Unit
	forced map[string]bool
}

// BuildView reads the project's state and, with Poll, the host's. It writes
// nothing and takes no lock. A finished brief or slice is left out unless All
// is set, it is the one named by ID, or the slice's latest pull request is
// still under review.
func BuildView(ctx context.Context, d Deps, opt ViewOptions) (View, error) {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	view := View{At: now().UTC(), Briefs: []BriefView{}, Followups: []Followup{}}
	scopes, forcedBrief, err := viewScopes(ctx, d, opt.ID)
	if err != nil {
		return View{}, err
	}

	var ids, observe []string
	for _, sc := range scopes {
		ids = append(ids, sc.brief.ID)
		for _, s := range sc.slices {
			ids = append(ids, s.ID)
			records, err := d.Units.PullRequests(ctx, s.ID)
			if err != nil {
				return View{}, err
			}
			if len(records) > 0 {
				observe = append(observe, s.ID)
			}
		}
	}
	statuses := map[string]units.Status{}
	if len(ids) > 0 {
		if statuses, err = d.Units.Statuses(ctx, ids); err != nil {
			return View{}, err
		}
	}

	res := ObserveResult{}
	if len(observe) > 0 {
		if res, err = Observe(ctx, d, ObserveOptions{Slices: observe, Poll: opt.Poll}); err != nil {
			return View{}, err
		}
	} else if opt.Poll {
		_, res.ProviderErr = buildProvider(ctx, d)
	}
	view.Provider = providerLine(opt.Poll, res)

	plan := Plan(res.Observations, PlanOptions{DeleteBranches: d.DeleteBranches})
	followups := map[string][]Followup{}
	for _, f := range plan {
		followups[f.Slice] = append(followups[f.Slice], f)
	}
	observed := map[string]Observation{}
	for _, o := range res.Observations {
		observed[o.Slice.ID] = o
	}

	for _, sc := range scopes {
		bv := BriefView{ID: sc.brief.ID, Title: sc.brief.Title, Status: string(statuses[sc.brief.ID]), Slices: []SliceView{}}
		for _, s := range sc.slices {
			status := statuses[s.ID]
			o := observed[s.ID]
			if !opt.All && !sc.forced[s.ID] && statusFinished(status) && !inReview(o) {
				continue
			}
			sv, err := sliceView(ctx, d, s, status, o, followups[s.ID])
			if err != nil {
				return View{}, err
			}
			bv.Slices = append(bv.Slices, sv)
		}
		if !opt.All && sc.brief.ID != forcedBrief && statusFinished(statuses[sc.brief.ID]) && len(bv.Slices) == 0 {
			continue
		}
		view.Briefs = append(view.Briefs, bv)
		for _, sv := range bv.Slices {
			view.Followups = append(view.Followups, sv.Followups...)
		}
	}
	return view, nil
}

// inReview reports whether the latest pull request of the observed slice is
// open or closed without a decision.
func inReview(o Observation) bool {
	if o.Slice.ID == "" {
		return false
	}
	return o.Record.State == units.PullRequestOpen ||
		(o.Record.State == units.PullRequestClosed && o.Record.Resolution == "")
}

func providerLine(poll bool, res ObserveResult) string {
	switch {
	case !poll:
		return ProviderNotPolled
	case res.ProviderErr != nil:
		return fmt.Sprintf("unavailable (%v)", res.ProviderErr)
	case res.RateLimited != nil && res.RateLimited.ResetAt.IsZero():
		return "rate limited until later"
	case res.RateLimited != nil:
		return "rate limited until " + res.RateLimited.ResetAt.UTC().Format(time.RFC3339)
	}
	return ProviderOK
}

// viewScopes lists the briefs in scope with their slices in position order.
// The second result is the ID of the brief named by id, if it is one.
func viewScopes(ctx context.Context, d Deps, id string) ([]briefScope, string, error) {
	var briefs []units.Unit
	only := ""
	forced := map[string]bool{}
	forcedBrief := ""
	if id == "" {
		all, err := d.Units.List(ctx, units.Filter{Level: units.LevelBrief})
		if err != nil {
			return nil, "", err
		}
		briefs = all
	} else {
		u, err := d.Units.Get(ctx, id)
		if err != nil {
			return nil, "", err
		}
		switch u.Level {
		case units.LevelBrief:
			briefs = []units.Unit{u}
			forcedBrief = u.ID
		case units.LevelSlice:
			parent, err := d.Units.Get(ctx, u.ParentID)
			if err != nil {
				return nil, "", err
			}
			briefs = []units.Unit{parent}
			only = u.ID
			forced[u.ID] = true
			forcedBrief = parent.ID
		default:
			return nil, "", fmt.Errorf("%s is a %s, not a brief or a slice: give the ID of a brief or a slice", u.ID, u.Level)
		}
	}
	sort.SliceStable(briefs, func(i, j int) bool { return briefs[i].Number < briefs[j].Number })
	var out []briefScope
	for _, b := range briefs {
		parent := b.ID
		slices, err := d.Units.List(ctx, units.Filter{Level: units.LevelSlice, ParentID: &parent})
		if err != nil {
			return nil, "", err
		}
		if only != "" {
			kept := slices[:0:0]
			for _, s := range slices {
				if s.ID == only {
					kept = append(kept, s)
				}
			}
			slices = kept
		}
		sort.SliceStable(slices, func(i, j int) bool {
			if slices[i].Position != slices[j].Position {
				return slices[i].Position < slices[j].Position
			}
			return slices[i].Number < slices[j].Number
		})
		out = append(out, briefScope{brief: b, slices: slices, forced: forced})
	}
	return out, forcedBrief, nil
}

func sliceView(ctx context.Context, d Deps, s units.Unit, status units.Status, o Observation, fs []Followup) (SliceView, error) {
	sv := SliceView{ID: s.ID, Title: s.Title, Status: string(status), Followups: []Followup{}}
	waiting, err := d.Units.Waiting(ctx, s.ID)
	if err != nil {
		return SliceView{}, err
	}
	for _, w := range waiting {
		sv.WaitingOn = append(sv.WaitingOn, w.ID)
	}
	sv.Followups = append(sv.Followups, fs...)
	if o.Slice.ID == "" {
		return sv, nil
	}
	pr := &PullRequestView{
		Number:     o.Record.Number,
		URL:        o.Record.URL,
		Recorded:   string(o.Record.State),
		Resolution: o.Record.Resolution,
	}
	if o.Polled && o.Err == nil {
		pr.Host = string(o.PullRequest.State)
		pr.Draft = o.PullRequest.Draft
		sv.NewFeedback = len(o.NewFeedback)
	}
	sv.PullRequest = pr
	if o.Err != nil {
		sv.Error = o.Err.Error()
	}
	return sv, nil
}
