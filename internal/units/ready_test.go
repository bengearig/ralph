package units_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func newTask(t *testing.T, s *units.Store, parent, body, criteria string) units.Unit {
	t.Helper()
	return create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: parent, Title: "t", Body: body, AcceptanceCriteria: criteria})
}

func sliceID(t *testing.T, s *units.Store) string {
	t.Helper()
	return slice(t, s, brief(t, s).ID).ID
}

func toTodo(t *testing.T, s *units.Store, u units.Unit) units.Unit {
	t.Helper()
	got, err := s.SetState(context.Background(), u.ID, u.Fingerprint, units.StateTodo, "")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func str(s string) *string { return &s }

func TestReadinessOrphanNeedsBodyOnly(t *testing.T) {
	if got := units.Readiness(units.Unit{Body: "do it"}); len(got) != 0 {
		t.Errorf("Readiness = %v, want none", got)
	}
	if got := units.Readiness(units.Unit{}); !slices.Equal(got, []string{"description"}) {
		t.Errorf("Readiness = %v", got)
	}
}

func TestReadinessSliceTaskNeedsBodyAndCriteria(t *testing.T) {
	tests := []struct {
		body, criteria string
		want           []string
	}{
		{"", "", []string{"description", "acceptance criteria"}},
		{"x", "", []string{"acceptance criteria"}},
		{"", "y", []string{"description"}},
		{"x", "y", nil},
	}
	for _, tc := range tests {
		got := units.Readiness(units.Unit{ParentID: "S-1", Body: tc.body, AcceptanceCriteria: tc.criteria})
		if !slices.Equal(got, tc.want) {
			t.Errorf("body %q criteria %q: Readiness = %v, want %v", tc.body, tc.criteria, got, tc.want)
		}
	}
}

func TestReadinessBlankMeansWhitespaceOnly(t *testing.T) {
	got := units.Readiness(units.Unit{ParentID: "S-1", Body: " \n\t ", AcceptanceCriteria: "  \n"})
	if len(got) != 2 {
		t.Errorf("Readiness = %v, want both missing", got)
	}
}

func TestPromoteUnreadyRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := newTask(t, s, sliceID(t, s), "", "")
	_, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateTodo, "go")
	var nre *units.NotReadyError
	if !errors.Is(err, units.ErrNotReady) || !errors.As(err, &nre) {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(nre.Missing, []string{"description", "acceptance criteria"}) || nre.Editing {
		t.Errorf("error = %+v", nre)
	}
	if want := "not ready: " + tk.ID + " needs a description and acceptance criteria before it can be TODO"; err.Error() != want {
		t.Errorf("message = %q, want %q", err, want)
	}
	got, _ := s.Get(ctx, tk.ID)
	if got.State != units.StateProposed || got.Fingerprint != tk.Fingerprint {
		t.Errorf("task = %+v", got)
	}
	if notes, _ := s.Notes(ctx, tk.ID); len(notes) != 0 {
		t.Errorf("notes = %+v", notes)
	}
}

func TestPromoteOnlyDescriptionMissing(t *testing.T) {
	s := newStore(t)
	tk := newTask(t, s, sliceID(t, s), "", "works")
	_, err := s.SetState(context.Background(), tk.ID, tk.Fingerprint, units.StateTodo, "")
	if want := "not ready: " + tk.ID + " needs a description before it can be TODO"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestPromoteReadyOrphanOK(t *testing.T) {
	s := newStore(t)
	tk := newTask(t, s, "", "do it", "")
	if got := toTodo(t, s, tk); got.State != units.StateTodo {
		t.Errorf("state = %s", got.State)
	}
}

func TestPromoteReadySliceTaskOK(t *testing.T) {
	s := newStore(t)
	tk := newTask(t, s, sliceID(t, s), "do it", "works")
	if got := toTodo(t, s, tk); got.State != units.StateTodo {
		t.Errorf("state = %s", got.State)
	}
}

func TestResumeFromBlockedRequiresReady(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := toTodo(t, s, newTask(t, s, sliceID(t, s), "do it", "works"))
	blocked, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateBlocked, "stuck")
	if err != nil {
		t.Fatal(err)
	}
	blanked, err := s.Update(ctx, blocked.ID, blocked.Fingerprint, units.Change{AcceptanceCriteria: str(" ")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetState(ctx, blanked.ID, blanked.Fingerprint, units.StateTodo, "")
	if !errors.Is(err, units.ErrNotReady) {
		t.Errorf("err = %v", err)
	}
}

func TestReturnToTodoFromInProgressStillWorksWhenReady(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := toTodo(t, s, newTask(t, s, sliceID(t, s), "do it", "works"))
	ip, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateInProgress, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SetState(ctx, ip.ID, ip.Fingerprint, units.StateTodo, ""); err != nil || got.State != units.StateTodo {
		t.Errorf("got %+v, err %v", got, err)
	}
}

func TestEditTodoToUnreadyRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := toTodo(t, s, newTask(t, s, sliceID(t, s), "do it", "works"))
	for _, tc := range []struct {
		name   string
		change units.Change
		item   string
	}{
		{"body", units.Change{Body: str(" \n")}, "a description"},
		{"criteria", units.Change{AcceptanceCriteria: str("")}, "acceptance criteria"},
	} {
		_, err := s.Update(ctx, tk.ID, tk.Fingerprint, tc.change)
		var nre *units.NotReadyError
		if !errors.Is(err, units.ErrNotReady) || !errors.As(err, &nre) || !nre.Editing {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		want := "not ready: this change would leave " + tk.ID + " without " + tc.item + "; move it back to PROPOSED to rewrite it"
		if err.Error() != want {
			t.Errorf("%s: message = %q, want %q", tc.name, err, want)
		}
		if got, _ := s.Get(ctx, tk.ID); got != tk {
			t.Errorf("%s: task changed: %+v", tc.name, got)
		}
	}
}

func TestEditInProgressToUnreadyRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := toTodo(t, s, newTask(t, s, sliceID(t, s), "do it", "works"))
	ip, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateInProgress, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ip.ID, ip.Fingerprint, units.Change{Body: str("")}); !errors.Is(err, units.ErrNotReady) {
		t.Errorf("err = %v", err)
	}
}

func TestEditProposedToUnreadyAllowed(t *testing.T) {
	s := newStore(t)
	tk := newTask(t, s, sliceID(t, s), "do it", "works")
	if _, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{Body: str(""), AcceptanceCriteria: str("")}); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestEditTodoStayingReadyAllowed(t *testing.T) {
	s := newStore(t)
	tk := toTodo(t, s, newTask(t, s, sliceID(t, s), "do it", "works"))
	got, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{Title: str("new"), Body: str("other")})
	if err != nil || got.Body != "other" {
		t.Errorf("got %+v, err %v", got, err)
	}
}

func TestEditDoesNotAffectOrphanCriteria(t *testing.T) {
	s := newStore(t)
	tk := toTodo(t, s, newTask(t, s, "", "do it", ""))
	if _, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{Title: str("new")}); err != nil {
		t.Errorf("err = %v", err)
	}
	got, err := s.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(context.Background(), tk.ID, got.Fingerprint, units.Change{AcceptanceCriteria: str("")}); err != nil {
		t.Errorf("clearing criteria: %v", err)
	}
}

func TestNotReadyOrderAfterTransitionCheck(t *testing.T) {
	s := newStore(t)
	tk := newTask(t, s, sliceID(t, s), "", "")
	_, err := s.SetState(context.Background(), tk.ID, tk.Fingerprint, units.StateDone, "")
	if !errors.Is(err, units.ErrInvalidTransition) || errors.Is(err, units.ErrNotReady) {
		t.Errorf("err = %v", err)
	}
}
