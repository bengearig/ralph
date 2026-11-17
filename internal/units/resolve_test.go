package units_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func recordClosed(t *testing.T, s *units.Store, slice string, n int) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordPullRequest(ctx, prRecord(slice, n)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPullRequestState(ctx, slice, n, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePullRequestSetsResolution(t *testing.T) {
	ctx := context.Background()
	d := testutil.NewDB(t, db.Migrations)
	s := units.New(d)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	sl := slice(t, s, brief(t, s).ID)
	recordClosed(t, s, sl.ID, 3)
	now = now.Add(time.Hour)
	if err := s.ResolvePullRequest(ctx, sl.ID, 3, "rearticulated"); err != nil {
		t.Fatal(err)
	}
	got, err := s.PullRequests(ctx, sl.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got[0].Resolution != "rearticulated" || got[0].State != units.PullRequestClosed || !got[0].UpdatedAt.Equal(now) || got[0].CreatedAt.Equal(now) {
		t.Errorf("record = %+v", got[0])
	}
}

func TestResolvePullRequestNeedsClosedUnresolved(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePullRequest(ctx, sl.ID, 1, "reopened"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("open: %v", err)
	}
	if err := s.SetPullRequestState(ctx, sl.ID, 1, units.PullRequestMerged); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePullRequest(ctx, sl.ID, 1, "reopened"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("merged: %v", err)
	}
	recordClosed(t, s, sl.ID, 2)
	if err := s.ResolvePullRequest(ctx, sl.ID, 2, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePullRequest(ctx, sl.ID, 2, "reopened"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("resolved: %v", err)
	}
	got, _ := s.PullRequests(ctx, sl.ID)
	if got[1].Resolution != "cancelled" {
		t.Errorf("resolution changed: %+v", got[1])
	}
}

func TestResolvePullRequestInvalidResolution(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	recordClosed(t, s, sl.ID, 1)
	for _, r := range []string{"", "merged", "Reopened"} {
		if err := s.ResolvePullRequest(ctx, sl.ID, 1, r); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%q: %v", r, err)
		}
	}
}

func TestResolvePullRequestUnknownRecord(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if err := s.ResolvePullRequest(ctx, sl.ID, 9, "reopened"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestDependentsDirectAndTransitive(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	b := brief(t, s)
	a, c, d := slice(t, s, b.ID), slice(t, s, b.ID), slice(t, s, b.ID)
	slice(t, s, b.ID)
	// d waits for a and for c, c waits for a; the last slice waits for nothing.
	addDep(t, s, d.ID, a.ID)
	addDep(t, s, c.ID, a.ID)
	addDep(t, s, d.ID, c.ID)
	got, err := s.Dependents(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{c.ID, d.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of a = %v, want %v", got, want)
	}
	got, _ = s.Dependents(ctx, c.ID)
	if want := []string{d.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of c = %v, want %v", got, want)
	}
}

func TestDependentsEmptyNonNil(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	got, err := s.Dependents(context.Background(), sl.ID)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %#v, %v", got, err)
	}
}

func TestDependentsOfNonSliceInvalid(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	tk := task(t, s, slice(t, s, b.ID).ID, "t")
	for _, id := range []string{b.ID, tk.ID} {
		if _, err := s.Dependents(context.Background(), id); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := s.Dependents(context.Background(), "S-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}
