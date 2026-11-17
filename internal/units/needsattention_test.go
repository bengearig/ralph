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

// doneSlice returns a store with a slice whose only task is DONE.
func doneSlice(t *testing.T) (*units.Store, *clockDB, units.Unit, units.Unit) {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	s := units.New(d)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tk := task(t, s, sl.ID, "t")
	move(t, s, tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	return s, &clockDB{d: d, s: s, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, b, sl
}

type clockDB struct {
	d   *db.DB
	s   *units.Store
	now time.Time
}

// record stores a pull request created one minute after the previous one.
func (c *clockDB) record(t *testing.T, sliceID string, n int, state units.PullRequestState) {
	t.Helper()
	c.now = c.now.Add(time.Minute)
	now := c.now
	c.s.Now = func() time.Time { return now }
	if err := c.s.RecordPullRequest(context.Background(), prRecord(sliceID, n)); err != nil {
		t.Fatal(err)
	}
	if err := c.s.SetPullRequestState(context.Background(), sliceID, n, state); err != nil {
		t.Fatal(err)
	}
}

func (c *clockDB) resolve(t *testing.T, sliceID string, n int, resolution string) {
	t.Helper()
	if _, err := c.d.SQL().ExecContext(context.Background(),
		"UPDATE pull_requests SET resolution = ? WHERE slice_id = ? AND number = ?", resolution, sliceID, n); err != nil {
		t.Fatal(err)
	}
}

func TestSetPullRequestStateChanges(t *testing.T) {
	ctx := context.Background()
	d := testutil.NewDB(t, db.Migrations)
	s := units.New(d)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 3)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := s.SetPullRequestState(ctx, sl.ID, 3, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	got, _ := s.PullRequests(ctx, sl.ID)
	if got[0].State != units.PullRequestClosed || !got[0].UpdatedAt.Equal(now) || !got[0].CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Errorf("record = %+v", got[0])
	}
}

func TestSetPullRequestStateSameStateNoOp(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 3)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := s.SetPullRequestState(ctx, sl.ID, 3, units.PullRequestOpen); err != nil {
		t.Fatal(err)
	}
	got, _ := s.PullRequests(ctx, sl.ID)
	if !got[0].UpdatedAt.Equal(got[0].CreatedAt) {
		t.Errorf("updated_at changed: %+v", got[0])
	}
}

func TestSetPullRequestStateUnknownRecord(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if err := s.SetPullRequestState(context.Background(), sl.ID, 9, units.PullRequestClosed); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if err := s.SetPullRequestState(context.Background(), "S-99", 1, units.PullRequestClosed); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown slice: err = %v", err)
	}
}

func TestSetPullRequestStateInvalidState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if err := s.RecordPullRequest(ctx, prRecord(sl.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPullRequestState(ctx, sl.ID, 1, "gone"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestNeedsAttentionWhenLatestClosedUnresolved(t *testing.T) {
	_, c, b, sl := doneSlice(t)
	if got := status(t, c.s, sl.ID); got != units.StatusDone {
		t.Fatalf("before: %s", got)
	}
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	if got := status(t, c.s, sl.ID); got != units.StatusNeedsAttention {
		t.Errorf("slice = %s", got)
	}
	if got := status(t, c.s, b.ID); got != units.StatusNeedsAttention {
		t.Errorf("brief = %s", got)
	}
}

func TestNeedsAttentionIgnoresOlderClosedPullRequest(t *testing.T) {
	_, c, _, sl := doneSlice(t)
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	c.record(t, sl.ID, 2, units.PullRequestOpen)
	if got := status(t, c.s, sl.ID); got != units.StatusInReview {
		t.Errorf("slice = %s", got)
	}
}

func TestNeedsAttentionTieBrokenByHigherNumber(t *testing.T) {
	_, c, _, sl := doneSlice(t)
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	if _, err := c.d.SQL().Exec("INSERT INTO pull_requests (slice_id, number, url, head, base, state, created_at, updated_at) SELECT slice_id, 2, url, head, base, 'open', created_at, updated_at FROM pull_requests WHERE number = 1"); err != nil {
		t.Fatal(err)
	}
	if got := status(t, c.s, sl.ID); got != units.StatusInReview {
		t.Errorf("slice = %s", got)
	}
}

func TestNeedsAttentionClearedByResolution(t *testing.T) {
	for _, res := range []string{"reopened", "rearticulated"} {
		_, c, _, sl := doneSlice(t)
		c.record(t, sl.ID, 1, units.PullRequestClosed)
		c.resolve(t, sl.ID, 1, res)
		if got := status(t, c.s, sl.ID); got != units.StatusDone {
			t.Errorf("%s: slice = %s", res, got)
		}
	}
}

func TestCancelledResolutionShowsCancelled(t *testing.T) {
	_, c, b, sl := doneSlice(t)
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	c.resolve(t, sl.ID, 1, "cancelled")
	if got := status(t, c.s, sl.ID); got != units.StatusCancelled {
		t.Errorf("slice = %s", got)
	}
	if got := status(t, c.s, b.ID); got != units.StatusCancelled {
		t.Errorf("brief = %s", got)
	}
}

func TestDeriveStatusNeedsAttentionBrief(t *testing.T) {
	const (
		N = units.StatusNeedsAttention
		B = units.StatusBlocked
		D = units.StatusDone
		C = units.StatusCancelled
		R = units.StatusReady
		I = units.StatusInProgress
	)
	tests := []struct {
		name string
		in   []units.Status
		want units.Status
	}{
		{"needs attention beats done and ready", []units.Status{D, R, I, N}, N},
		{"blocked still wins", []units.Status{N, B}, B},
		{"cancelled ignored", []units.Status{C, N}, N},
		{"only cancelled", []units.Status{C, C}, C},
	}
	for _, tc := range tests {
		if got := units.DeriveStatus(units.LevelBrief, tc.in); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDependencyStaysUnsatisfiedWhileNeedsAttention(t *testing.T) {
	ctx := context.Background()
	_, c, b, sl := doneSlice(t)
	waiter := slice(t, c.s, b.ID)
	addDep(t, c.s, waiter.ID, sl.ID)
	if w, err := c.s.Waiting(ctx, waiter.ID); err != nil || len(w) != 0 {
		t.Fatalf("before: %+v, %v", w, err)
	}
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	w, err := c.s.Waiting(ctx, waiter.ID)
	if err != nil || len(w) != 1 || w[0].ID != sl.ID || w[0].Status != units.StatusNeedsAttention {
		t.Errorf("waiting = %+v, %v", w, err)
	}
}

func TestStatusesBatchWithOverlay(t *testing.T) {
	_, c, b, sl := doneSlice(t)
	c.record(t, sl.ID, 1, units.PullRequestClosed)
	ids := []string{b.ID, sl.ID, "T-1"}
	got, err := c.s.Statuses(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]units.Status{b.ID: units.StatusNeedsAttention, sl.ID: units.StatusNeedsAttention, "T-1": units.StatusDone}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %s, want %s", id, got[id], w)
		}
	}
}
