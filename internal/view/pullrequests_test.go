package view

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func recordPR(t *testing.T, svc units.Service, n int, state units.PullRequestState) {
	t.Helper()
	err := svc.RecordPullRequest(context.Background(), units.PullRequestRecord{
		SliceID: "S-1", Number: n, URL: "https://x.test/pull/" + string(rune('0'+n)), Head: "h", Base: "main", State: state,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnitViewPullRequests(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()
	v, err := UnitDetail(ctx, svc, "S-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if v.PullRequests != nil || strings.Contains(string(raw), "pull_requests") {
		t.Errorf("pull requests shown with none: %s", raw)
	}
	recordPR(t, svc, 1, units.PullRequestClosed)
	recordPR(t, svc, 2, units.PullRequestOpen)
	v, err = UnitDetail(ctx, svc, "S-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(v.PullRequests)
	want := `[{"number":1,"url":"https://x.test/pull/1","state":"closed","resolution":""},{"number":2,"url":"https://x.test/pull/2","state":"open","resolution":""}]`
	if string(raw) != want {
		t.Errorf("pull_requests = %s", raw)
	}
	whole, _ := json.Marshal(v)
	if !strings.Contains(string(whole), `"pull_requests":[`) {
		t.Errorf("key missing: %s", whole)
	}
	task, err := UnitDetail(ctx, svc, "T-1")
	if err != nil || task.PullRequests != nil {
		t.Errorf("task = %+v, %v", task.PullRequests, err)
	}
}

func TestSummaryShowsNeedsAttentionWithoutAll(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		u, err := svc.Get(ctx, "T-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetState(ctx, "T-1", u.Fingerprint, st, "r"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 0 {
		t.Fatalf("finished slice shown: %+v", got.Briefs)
	}
	recordPR(t, svc, 1, units.PullRequestClosed)
	got, err = Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || got.Briefs[0].Status != "NEEDS_ATTENTION" ||
		len(got.Briefs[0].Slices) != 1 || got.Briefs[0].Slices[0].Status != "NEEDS_ATTENTION" {
		t.Errorf("briefs = %+v", got.Briefs)
	}
}

func TestNoPullRequestsOutputUnchanged(t *testing.T) {
	svc := seed(t)
	for _, id := range []string{"B-1", "S-1", "T-1", "T-2"} {
		v, err := UnitDetail(context.Background(), svc, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "pull_requests") {
			t.Errorf("%s: %s", id, raw)
		}
	}
}
