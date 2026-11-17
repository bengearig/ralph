package units_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func prRecord(slice string, n int) units.PullRequestRecord {
	return units.PullRequestRecord{SliceID: slice, Number: n, URL: "https://example.test/pr/1", Head: "ralph/s-1", Base: "main", State: units.PullRequestOpen}
}

func TestRecordPullRequestInsertAndList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 7)); err != nil {
		t.Fatal(err)
	}
	got, err := s.PullRequests(ctx, sl.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	r := got[0]
	if r.SliceID != sl.ID || r.Number != 7 || r.URL != "https://example.test/pr/1" || r.Head != "ralph/s-1" || r.Base != "main" ||
		r.State != units.PullRequestOpen || r.Resolution != "" || r.CreatedAt.IsZero() || !r.CreatedAt.Equal(r.UpdatedAt) {
		t.Errorf("record = %+v", r)
	}
}

func TestRecordPullRequestUpdateKeepsCreatedAndResolution(t *testing.T) {
	ctx := context.Background()
	d := testutil.NewDB(t, db.Migrations)
	s := units.New(d)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(ctx, "UPDATE pull_requests SET resolution = 'reopened'"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	upd := prRecord(sl.ID, 1)
	upd.URL, upd.Head, upd.Base, upd.State = "https://example.test/pr/1b", "other", "dev", units.PullRequestClosed
	if err := s.RecordPullRequest(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got, err := s.PullRequests(ctx, sl.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	r := got[0]
	if r.URL != upd.URL || r.Head != "other" || r.Base != "dev" || r.State != units.PullRequestClosed {
		t.Errorf("not updated: %+v", r)
	}
	if r.Resolution != "reopened" || !r.CreatedAt.Equal(now.Add(-time.Hour)) || !r.UpdatedAt.Equal(now) {
		t.Errorf("kept fields wrong: %+v", r)
	}
}

func TestRecordPullRequestValidation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tk := task(t, s, sl.ID, "t")
	with := func(f func(*units.PullRequestRecord)) units.PullRequestRecord {
		r := prRecord(sl.ID, 1)
		f(&r)
		return r
	}
	invalid := map[string]units.PullRequestRecord{
		"brief":     with(func(r *units.PullRequestRecord) { r.SliceID = b.ID }),
		"task":      with(func(r *units.PullRequestRecord) { r.SliceID = tk.ID }),
		"number 0":  with(func(r *units.PullRequestRecord) { r.Number = 0 }),
		"empty url": with(func(r *units.PullRequestRecord) { r.URL = "" }),
		"no head":   with(func(r *units.PullRequestRecord) { r.Head = "" }),
		"no base":   with(func(r *units.PullRequestRecord) { r.Base = "" }),
		"bad state": with(func(r *units.PullRequestRecord) { r.State = "weird" }),
	}
	for name, rec := range invalid {
		if err := s.RecordPullRequest(ctx, rec); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := s.RecordPullRequest(ctx, with(func(r *units.PullRequestRecord) { r.SliceID = "S-99" })); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown unit: err = %v, want ErrNotFound", err)
	}
	if _, err := s.PullRequests(ctx, tk.ID); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("PullRequests of a task: err = %v", err)
	}
}

func TestPullRequestsOrderedOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	sl := slice(t, s, brief(t, s).ID)
	for _, step := range []struct {
		n   int
		add time.Duration
	}{{9, 0}, {3, time.Second}, {5, 0}, {4, time.Second}} {
		now = now.Add(step.add)
		if err := s.RecordPullRequest(ctx, prRecord(sl.ID, step.n)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PullRequests(ctx, sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	for _, r := range got {
		order = append(order, r.Number)
	}
	want := []int{9, 3, 5, 4}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestPullRequestsEmptyNonNil(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	got, err := s.PullRequests(context.Background(), sl.ID)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v", got, err)
	}
}
