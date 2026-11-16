package units_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func linkTask(t *testing.T, s *units.Store) units.Unit {
	return task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
}

func TestAddLinkAndList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := linkTask(t, s)
	for _, l := range []struct {
		k units.LinkKind
		t string
	}{{units.LinkExternal, "GH-9"}, {units.LinkDocument, "D-2"}, {units.LinkDocument, "D-1"}} {
		if err := s.AddLink(ctx, tk.ID, l.k, l.t); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Links(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"document D-1", "document D-2", "external GH-9"}
	if len(got) != len(want) {
		t.Fatalf("links = %+v", got)
	}
	for i, l := range got {
		if string(l.Kind)+" "+l.Target != want[i] || l.CreatedAt.IsZero() {
			t.Errorf("link %d = %+v, want %s", i, l, want[i])
		}
	}
}

func TestAddLinkIsIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := linkTask(t, s)
	for range 2 {
		if err := s.AddLink(ctx, tk.ID, units.LinkDocument, "D-1"); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Links(ctx, tk.ID); len(got) != 1 {
		t.Fatalf("links = %+v, want one", got)
	}
}

func TestRemoveLink(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := linkTask(t, s)
	_ = s.AddLink(ctx, tk.ID, units.LinkDocument, "D-1")
	_ = s.AddLink(ctx, tk.ID, units.LinkExternal, "D-1")
	if err := s.RemoveLink(ctx, tk.ID, units.LinkDocument, "D-1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Links(ctx, tk.ID)
	if len(got) != 1 || got[0].Kind != units.LinkExternal {
		t.Fatalf("links = %+v", got)
	}
}

func TestRemoveMissingLinkIsNoop(t *testing.T) {
	s := newStore(t)
	tk := linkTask(t, s)
	if err := s.RemoveLink(context.Background(), tk.ID, units.LinkDocument, "nope"); err != nil {
		t.Fatal(err)
	}
}

func TestLinkUnknownUnit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.AddLink(ctx, "T-99", units.LinkDocument, "D-1"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("AddLink err = %v", err)
	}
	if err := s.RemoveLink(ctx, "T-99", units.LinkDocument, "D-1"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("RemoveLink err = %v", err)
	}
	if _, err := s.Links(ctx, "T-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("Links err = %v", err)
	}
}

func TestLinkValidation(t *testing.T) {
	s := newStore(t)
	tk := linkTask(t, s)
	cases := map[string]struct {
		kind   units.LinkKind
		target string
	}{
		"unknown kind": {"url", "x"},
		"empty target": {units.LinkDocument, ""},
		"space":        {units.LinkDocument, "a b"},
		"newline":      {units.LinkExternal, "a\nb"},
		"control":      {units.LinkExternal, "a\x00b"},
		"too long":     {units.LinkExternal, strings.Repeat("x", 513)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.AddLink(context.Background(), tk.ID, c.kind, c.target); !errors.Is(err, units.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if err := s.AddLink(context.Background(), tk.ID, units.LinkExternal, strings.Repeat("x", 512)); err != nil {
		t.Errorf("512 characters: %v", err)
	}
}

func TestLinkTargetNotChecked(t *testing.T) {
	s := newStore(t)
	tk := linkTask(t, s)
	if err := s.AddLink(context.Background(), tk.ID, units.LinkDocument, "D-99999"); err != nil {
		t.Fatal(err)
	}
}

func TestLinksDoNotChangeFingerprint(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := linkTask(t, s)
	_ = s.AddLink(ctx, tk.ID, units.LinkDocument, "D-1")
	after, _ := s.Get(ctx, tk.ID)
	if after.Fingerprint != tk.Fingerprint {
		t.Errorf("fingerprint changed: %s -> %s", tk.Fingerprint, after.Fingerprint)
	}
}

func TestLinksDoNotChangeUpdatedAt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := linkTask(t, s)
	_ = s.AddLink(ctx, tk.ID, units.LinkDocument, "D-1")
	_ = s.RemoveLink(ctx, tk.ID, units.LinkDocument, "D-1")
	after, _ := s.Get(ctx, tk.ID)
	if !after.UpdatedAt.Equal(tk.UpdatedAt) {
		t.Errorf("updated_at changed: %v -> %v", tk.UpdatedAt, after.UpdatedAt)
	}
}

func TestLinksOnAnyLevel(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	orphan := task(t, s, "", "orphan")
	for _, u := range []units.Unit{b, sl, orphan} {
		if err := s.AddLink(ctx, u.ID, units.LinkExternal, "X-1"); err != nil {
			t.Fatalf("%s: %v", u.ID, err)
		}
		if got, _ := s.Links(ctx, u.ID); len(got) != 1 {
			t.Errorf("%s links = %+v", u.ID, got)
		}
	}
}

func TestLinksNoneReturnsEmptySlice(t *testing.T) {
	s := newStore(t)
	tk := linkTask(t, s)
	got, err := s.Links(context.Background(), tk.ID)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("links = %#v, err = %v", got, err)
	}
}
