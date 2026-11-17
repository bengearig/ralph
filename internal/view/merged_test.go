package view

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func finishT1(t *testing.T, svc units.Service) {
	t.Helper()
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
}

func TestStatusMergedHiddenWithoutAll(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()
	recordPR(t, svc, 1, units.PullRequestMerged)
	got, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 0 {
		t.Errorf("merged slice shown: %+v", got.Briefs)
	}
	got, err = Summary(ctx, svc, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || got.Briefs[0].Status != "DONE" || got.Briefs[0].Slices[0].Status != "MERGED" {
		t.Errorf("with all: %+v", got.Briefs)
	}
}

func TestStatusInReviewShown(t *testing.T) {
	svc := seed(t)
	finishT1(t, svc)
	recordPR(t, svc, 1, units.PullRequestOpen)
	got, err := Summary(context.Background(), svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || got.Briefs[0].Status != "IN_PROGRESS" ||
		len(got.Briefs[0].Slices) != 1 || got.Briefs[0].Slices[0].Status != "IN_REVIEW" {
		t.Errorf("briefs = %+v", got.Briefs)
	}
}
