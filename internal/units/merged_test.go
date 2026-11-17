package units_test

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func recordPR(t *testing.T, s *units.Store, sliceID string, n int, state units.PullRequestState) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordPullRequest(ctx, prRecord(sliceID, n)); err != nil {
		t.Fatal(err)
	}
	if state != units.PullRequestOpen {
		if err := s.SetPullRequestState(ctx, sliceID, n, state); err != nil {
			t.Fatal(err)
		}
	}
}

func wantStatus(t *testing.T, s *units.Store, id string, want units.Status) {
	t.Helper()
	got, err := s.Status(context.Background(), id)
	if err != nil || got != want {
		t.Errorf("status of %s = %q, %v; want %q", id, got, err, want)
	}
}

func finishedSlice(t *testing.T, s *units.Store, parent string) units.Unit {
	t.Helper()
	sl := slice(t, s, parent)
	finish(t, s, todoTask(t, s, sl.ID).ID)
	return sl
}

func TestMergedStatusFromLatestRecord(t *testing.T) {
	s := newStore(t)
	sl := finishedSlice(t, s, brief(t, s).ID)
	recordPR(t, s, sl.ID, 1, units.PullRequestMerged)
	wantStatus(t, s, sl.ID, units.StatusMerged)
}

func TestInReviewWhenDoneAndOpen(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := finishedSlice(t, s, b.ID)
	recordPR(t, s, sl.ID, 1, units.PullRequestOpen)
	wantStatus(t, s, sl.ID, units.StatusInReview)

	ready := slice(t, s, b.ID)
	todoTask(t, s, ready.ID)
	recordPR(t, s, ready.ID, 1, units.PullRequestOpen)
	wantStatus(t, s, ready.ID, units.StatusReady)

	progress := slice(t, s, b.ID)
	advance(t, s, todoTask(t, s, progress.ID).ID, units.StateInProgress)
	recordPR(t, s, progress.ID, 1, units.PullRequestOpen)
	wantStatus(t, s, progress.ID, units.StatusInProgress)
}

func TestMergedWinsOverTasks(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	advance(t, s, todoTask(t, s, sl.ID).ID, units.StateInProgress)
	recordPR(t, s, sl.ID, 1, units.PullRequestMerged)
	wantStatus(t, s, sl.ID, units.StatusMerged)
}

func TestDeriveStatusBriefMergedCountsAsDone(t *testing.T) {
	got := units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusMerged, units.StatusMerged})
	if got != units.StatusDone {
		t.Errorf("merged only = %q", got)
	}
	got = units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusMerged, units.StatusDone, units.StatusCancelled})
	if got != units.StatusDone {
		t.Errorf("merged and done = %q", got)
	}
	got = units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusMerged, units.StatusBlocked})
	if got != units.StatusBlocked {
		t.Errorf("blocked wins = %q", got)
	}
	got = units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusMerged, units.StatusNeedsAttention})
	if got != units.StatusNeedsAttention {
		t.Errorf("needs attention wins = %q", got)
	}
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	recordPR(t, s, sl.ID, 1, units.PullRequestMerged)
	wantStatus(t, s, b.ID, units.StatusDone)
}

func TestDeriveStatusBriefInReviewIsInProgress(t *testing.T) {
	got := units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusInReview, units.StatusMerged})
	if got != units.StatusInProgress {
		t.Errorf("in review = %q", got)
	}
	got = units.DeriveStatus(units.LevelBrief, []units.Status{units.StatusInReview, units.StatusBlocked})
	if got != units.StatusBlocked {
		t.Errorf("blocked wins = %q", got)
	}
}

func depOf(t *testing.T, s *units.Store, waiting, on string) units.Dependency {
	t.Helper()
	addDep(t, s, waiting, on)
	deps, err := s.Dependencies(context.Background(), waiting)
	if err != nil || len(deps) != 1 {
		t.Fatalf("deps = %+v, %v", deps, err)
	}
	return deps[0]
}

func TestDependencySatisfiedWhenMerged(t *testing.T) {
	s, b, a, w := twoSlices(t)
	_ = b
	recordPR(t, s, a.ID, 1, units.PullRequestMerged)
	if d := depOf(t, s, w.ID, a.ID); !d.Satisfied || d.Status != units.StatusMerged {
		t.Errorf("dependency = %+v", d)
	}
	waiting, err := s.Waiting(context.Background(), w.ID)
	if err != nil || len(waiting) != 0 {
		t.Errorf("waiting = %+v, %v", waiting, err)
	}
}

func TestDependencyNotSatisfiedWhileInReview(t *testing.T) {
	s, b, _, w := twoSlices(t)
	done := finishedSlice(t, s, b.ID)
	recordPR(t, s, done.ID, 1, units.PullRequestOpen)
	if d := depOf(t, s, w.ID, done.ID); d.Satisfied || d.Status != units.StatusInReview {
		t.Errorf("dependency = %+v", d)
	}
}

func TestDependencySatisfiedWhenDoneWithoutPullRequest(t *testing.T) {
	s, b, _, w := twoSlices(t)
	done := finishedSlice(t, s, b.ID)
	if d := depOf(t, s, w.ID, done.ID); !d.Satisfied || d.Status != units.StatusDone {
		t.Errorf("dependency = %+v", d)
	}
}

func TestDependencyNotSatisfiedAfterResolvedClosedRecord(t *testing.T) {
	ctx := context.Background()
	s, b, _, w := twoSlices(t)
	done := finishedSlice(t, s, b.ID)
	recordPR(t, s, done.ID, 1, units.PullRequestClosed)
	if err := s.ResolvePullRequest(ctx, done.ID, 1, "reopened"); err != nil {
		t.Fatal(err)
	}
	if d := depOf(t, s, w.ID, done.ID); d.Satisfied || d.Status != units.StatusDone {
		t.Errorf("dependency = %+v", d)
	}
}
