package taskmanagertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
)

// Controls change what is behind a System. A nil control makes the checks that
// need it skip.
type Controls struct {
	// RejectCredential, RateLimit and Down make the system answer as an
	// unauthorized, rate-limited or unavailable one until Heal is called.
	RejectCredential func()
	RateLimit        func(until time.Time)
	Down             func()
	Heal             func()
	// Delete removes a ticket and Edit changes its title and body, as a person
	// in the system would; ForceStatus puts a ticket in a status whatever the
	// workflow says.
	Delete      func(key string)
	Edit        func(key string, f taskmanager.Fields)
	ForceStatus func(key, status string)
}

// Subject is a system together with its controls and the names the checks use.
// An empty name takes its default.
type Subject struct {
	System   taskmanager.System
	Controls Controls

	// Type is the ticket type the checks create, and ParentType one that may
	// be a parent of it; defaults Task and Feature.
	Type       string
	ParentType string
	// Statuses are the initial status, a status with a direct transition from
	// it, and a status with a direct transition from that one and none from
	// the initial; defaults To Do, In Progress and Done. The checks pass them
	// as the flow.
	Statuses []string
	// Plan is a plan Schedule accepts; the check that needs one is skipped
	// when it is empty.
	Plan string
	// Without names the methods the system does not implement yet, Move or
	// Schedule: the checks of those methods skip and the checks that call
	// every method leave them out.
	Without []string
}

func (s Subject) lacks(method string) bool { return slices.Contains(s.Without, method) }

func (s Subject) ticketType() string { return orDefault(s.Type, "Task") }
func (s Subject) parentType() string { return orDefault(s.ParentType, "Feature") }
func (s Subject) statuses() []string {
	if len(s.Statuses) == 3 {
		return s.Statuses
	}
	return []string{"To Do", "In Progress", "Done"}
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// MissingControls names the controls that are not set, so a test can insist
// that no check is skipped for want of one.
func (s Subject) MissingControls() []string {
	var out []string
	for _, c := range []string{ctlReject, ctlRateLimit, ctlDown, ctlHeal, ctlDelete, ctlEdit, ctlForceStatus, ctlPlan} {
		if missing(s, c) {
			out = append(out, c)
		}
	}
	return out
}

const (
	ctlReject      = "RejectCredential"
	ctlRateLimit   = "RateLimit"
	ctlDown        = "Down"
	ctlHeal        = "Heal"
	ctlDelete      = "Delete"
	ctlEdit        = "Edit"
	ctlForceStatus = "ForceStatus"
	ctlPlan        = "Plan"
)

func missing(s Subject, control string) bool {
	switch control {
	case ctlReject:
		return s.Controls.RejectCredential == nil
	case ctlRateLimit:
		return s.Controls.RateLimit == nil
	case ctlDown:
		return s.Controls.Down == nil
	case ctlHeal:
		return s.Controls.Heal == nil
	case ctlDelete:
		return s.Controls.Delete == nil
	case ctlEdit:
		return s.Controls.Edit == nil
	case ctlForceStatus:
		return s.Controls.ForceStatus == nil
	case ctlPlan:
		return s.Plan == ""
	}
	return false
}

// runT is the part of *testing.T the suite needs, so that tests of the suite
// can run it against a recorder.
type runT interface {
	testing.TB
	Run(name string, f func(t runT)) bool
}

type stdT struct{ *testing.T }

func (s stdT) Run(name string, f func(t runT)) bool {
	return s.T.Run(name, func(t *testing.T) { f(stdT{t}) })
}

type check struct {
	name  string
	needs []string
	run   func(t runT, s Subject)
}

var checks = []check{
	{"Check", nil, checkWorks},
	{"Types", nil, typesHoldCheckTypes},
	{"CreateReturnsKeyStatusAndRef", nil, createReturns},
	{"GetReturnsNormalizedFields", nil, getNormalized},
	{"GetOmitsUnknownKey", nil, getOmitsUnknown},
	{"GetOmitsDeletedKey", []string{ctlDelete}, getOmitsDeleted},
	{"GetSeesEdit", []string{ctlEdit}, getSeesEdit},
	{"UpdateChangesFields", nil, updateChanges},
	{"FindByRef", nil, findByRef},
	{"FindUnknownRef", nil, findUnknown},
	{"CreateWithParent", nil, createWithParent},
	{"MoveToCurrentStatus", nil, moveCurrent},
	{"MoveDirect", nil, moveDirect},
	{"MoveThroughFlow", nil, moveThroughFlow},
	{"MoveUnreachable", nil, moveUnreachable},
	{"ScheduleKnownPlan", []string{ctlPlan}, scheduleKnown},
	{"ScheduleUnknownPlan", nil, scheduleUnknown},
	{"NormalizeIdempotent", nil, normalizeIdempotent},
	{"RejectedCredential", []string{ctlReject, ctlHeal}, rejectedCredential},
	{"RateLimited", []string{ctlRateLimit, ctlHeal}, rateLimited},
	{"Unavailable", []string{ctlDown, ctlHeal}, unavailable},
}

// Run runs every check as a subtest against s.
func Run(t *testing.T, s Subject) {
	t.Helper()
	run(stdT{t}, s)
}

func run(t runT, s Subject) {
	for _, c := range checks {
		t.Run(c.name, func(t runT) {
			if s.System == nil {
				t.Fatal("Subject.System is nil")
			}
			for _, m := range s.Without {
				if strings.HasPrefix(c.name, m) {
					t.Skipf("subject does not implement %s yet", m)
				}
			}
			for _, need := range c.needs {
				if missing(s, need) {
					t.Skipf("subject control %s is not set", need)
				}
			}
			c.run(t, s)
		})
	}
}

var (
	ctx     = context.Background()
	counter int
)

func ref(name string) string {
	counter++
	return fmt.Sprintf("ralph-conformance-%s-%d-%d", name, time.Now().UnixNano(), counter)
}

const longTitle = "Conformance title " +
	"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" +
	"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" +
	"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

func create(t testing.TB, s Subject, name string, n taskmanager.New) taskmanager.Ticket {
	t.Helper()
	if n.Type == "" {
		n.Type = s.ticketType()
	}
	if n.Title == "" {
		n.Title = "Conformance " + name
	}
	if n.Ref == "" {
		n.Ref = ref(name)
	}
	tk, err := s.System.Create(ctx, n)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return tk
}

func get(t testing.TB, s Subject, key string) (taskmanager.Ticket, bool) {
	t.Helper()
	got, err := s.System.Get(ctx, []string{key})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	tk, ok := got[key]
	return tk, ok
}

func mustGet(t testing.TB, s Subject, key string) taskmanager.Ticket {
	t.Helper()
	tk, ok := get(t, s, key)
	if !ok {
		t.Fatalf("get %s: ticket is absent", key)
	}
	return tk
}

func checkWorks(t runT, s Subject) {
	if _, err := s.System.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func typesHoldCheckTypes(t runT, s Subject) {
	types, err := s.System.Types(ctx)
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	if len(types) == 0 {
		t.Fatal("Types is empty")
	}
	for _, want := range []string{s.ticketType(), s.parentType()} {
		if !slices.Contains(types, want) {
			t.Errorf("Types = %q, want it to hold %q", types, want)
		}
	}
}

func createReturns(t runT, s Subject) {
	r := ref("create")
	tk := create(t, s, "create", taskmanager.New{Ref: r})
	if tk.Key == "" {
		t.Error("Create returned no key")
	}
	if tk.Ref != r {
		t.Errorf("Ref = %q, want %q", tk.Ref, r)
	}
	if want := s.statuses()[0]; tk.Status != want {
		t.Errorf("Status = %q, want the initial status %q", tk.Status, want)
	}
	if tk.Type != s.ticketType() {
		t.Errorf("Type = %q, want %q", tk.Type, s.ticketType())
	}
}

func getNormalized(t runT, s Subject) {
	in := taskmanager.Fields{Title: longTitle, Body: "Body text\n\n- one\n- two  \n\n  "}
	tk := create(t, s, "get", taskmanager.New{Title: in.Title, Body: in.Body})
	want := s.System.Normalize(in)
	got := mustGet(t, s, tk.Key)
	if got.Title != want.Title || got.Body != want.Body {
		t.Errorf("Get fields = %q / %q, want %q / %q", got.Title, got.Body, want.Title, want.Body)
	}
	if tk.Title != want.Title || tk.Body != want.Body {
		t.Errorf("Create fields = %q / %q, want what the system holds, %q / %q", tk.Title, tk.Body, want.Title, want.Body)
	}
	if got.Key != tk.Key || got.Ref != tk.Ref || got.Status != tk.Status || got.Type != tk.Type {
		t.Errorf("Get = %+v, Create = %+v", got, tk)
	}
}

func unknownKey(tk taskmanager.Ticket) string {
	if i := strings.LastIndex(tk.Key, "-"); i > 0 {
		return tk.Key[:i] + "-99999999"
	}
	return tk.Key + "-99999999"
}

func getOmitsUnknown(t runT, s Subject) {
	tk := create(t, s, "unknown", taskmanager.New{})
	got, err := s.System.Get(ctx, []string{tk.Key, unknownKey(tk)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got[unknownKey(tk)]; ok || len(got) != 1 {
		t.Errorf("Get = %+v, want only %s", got, tk.Key)
	}
}

func getOmitsDeleted(t runT, s Subject) {
	tk := create(t, s, "deleted", taskmanager.New{})
	s.Controls.Delete(tk.Key)
	if _, ok := get(t, s, tk.Key); ok {
		t.Errorf("Get returned %s after it was deleted", tk.Key)
	}
}

func getSeesEdit(t runT, s Subject) {
	tk := create(t, s, "edited", taskmanager.New{})
	f := taskmanager.Fields{Title: "Edited by a person", Body: "Edited body"}
	s.Controls.Edit(tk.Key, f)
	want := s.System.Normalize(f)
	if got := mustGet(t, s, tk.Key); got.Title != want.Title || got.Body != want.Body {
		t.Errorf("Get fields = %q / %q, want %q / %q", got.Title, got.Body, want.Title, want.Body)
	}
}

func updateChanges(t runT, s Subject) {
	tk := create(t, s, "update", taskmanager.New{Body: "before"})
	f := taskmanager.Fields{Title: longTitle + " updated", Body: "After the update\n\n  "}
	if err := s.System.Update(ctx, tk.Key, f); err != nil {
		t.Fatalf("Update: %v", err)
	}
	want := s.System.Normalize(f)
	if got := mustGet(t, s, tk.Key); got.Title != want.Title || got.Body != want.Body {
		t.Errorf("Get fields = %q / %q, want %q / %q", got.Title, got.Body, want.Title, want.Body)
	}
}

func findByRef(t runT, s Subject) {
	tk := create(t, s, "find", taskmanager.New{})
	got, err := s.System.Find(ctx, tk.Ref)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got == nil || got.Key != tk.Key || got.Ref != tk.Ref {
		t.Errorf("Find = %+v, want %s", got, tk.Key)
	}
}

func findUnknown(t runT, s Subject) {
	got, err := s.System.Find(ctx, ref("never-created"))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != nil {
		t.Errorf("Find = %+v, want nil", got)
	}
}

func createWithParent(t runT, s Subject) {
	parent := create(t, s, "parent", taskmanager.New{Type: s.parentType()})
	child := create(t, s, "child", taskmanager.New{Parent: parent.Key})
	if child.Parent != parent.Key {
		t.Errorf("Create Parent = %q, want %q", child.Parent, parent.Key)
	}
	if got := mustGet(t, s, child.Key); got.Parent != parent.Key {
		t.Errorf("Get Parent = %q, want %q", got.Parent, parent.Key)
	}
}

func moveCurrent(t runT, s Subject) {
	tk := create(t, s, "move-same", taskmanager.New{})
	st := s.statuses()
	m, err := s.System.Move(ctx, tk.Key, st[0], st)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if m.From != st[0] || m.To != st[0] || len(m.Via) != 0 {
		t.Errorf("Move = %+v, want no change at %q", m, st[0])
	}
	if got := mustGet(t, s, tk.Key); got.Status != st[0] {
		t.Errorf("Status = %q, want %q", got.Status, st[0])
	}
}

func moveDirect(t runT, s Subject) {
	tk := create(t, s, "move-direct", taskmanager.New{})
	st := s.statuses()
	m, err := s.System.Move(ctx, tk.Key, st[1], nil)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if m.From != st[0] || m.To != st[1] || len(m.Via) != 0 {
		t.Errorf("Move = %+v, want %q to %q with nothing between", m, st[0], st[1])
	}
	if got := mustGet(t, s, tk.Key); got.Status != st[1] {
		t.Errorf("Status = %q, want %q", got.Status, st[1])
	}
}

func moveThroughFlow(t runT, s Subject) {
	tk := create(t, s, "move-flow", taskmanager.New{})
	st := s.statuses()
	m, err := s.System.Move(ctx, tk.Key, st[2], st)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if m.From != st[0] || m.To != st[2] || !slices.Equal(m.Via, st[1:2]) {
		t.Errorf("Move = %+v, want %q to %q via %q", m, st[0], st[2], st[1])
	}
	if got := mustGet(t, s, tk.Key); got.Status != st[2] {
		t.Errorf("Status = %q, want %q", got.Status, st[2])
	}
}

func moveUnreachable(t runT, s Subject) {
	tk := create(t, s, "move-unreachable", taskmanager.New{})
	st := s.statuses()
	target := "Conformance status that does not exist"
	_, err := s.System.Move(ctx, tk.Key, target, nil)
	if err == nil {
		t.Fatal("Move to an unreachable status succeeded")
	}
	if !strings.Contains(err.Error(), st[1]) {
		t.Errorf("Move error %q does not list the reachable status %q", err, st[1])
	}
	if got := mustGet(t, s, tk.Key); got.Status != st[0] {
		t.Errorf("Status = %q after a failed move, want %q", got.Status, st[0])
	}
}

func scheduleKnown(t runT, s Subject) {
	tk := create(t, s, "schedule", taskmanager.New{})
	if err := s.System.Schedule(ctx, tk.Key, s.Plan); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
}

func scheduleUnknown(t runT, s Subject) {
	tk := create(t, s, "schedule-unknown", taskmanager.New{})
	err := s.System.Schedule(ctx, tk.Key, "Conformance plan that does not exist")
	if !errors.Is(err, taskmanager.ErrNotFound) {
		t.Errorf("Schedule err = %v, want ErrNotFound", err)
	}
}

func normalizeIdempotent(t runT, s Subject) {
	for _, f := range []taskmanager.Fields{
		{},
		{Title: "plain", Body: "plain"},
		{Title: longTitle, Body: "text\n\n  \n"},
		{Title: strings.Repeat("é", 300), Body: "  leading and trailing  \n\n"},
	} {
		once := s.System.Normalize(f)
		if twice := s.System.Normalize(once); twice != once {
			t.Errorf("Normalize(%+v) = %+v, normalized again %+v", f, once, twice)
		}
	}
}

// everyOperation calls each method that reaches the system and hands its
// error to verify.
func everyOperation(t runT, s Subject, key string, verify func(op string, err error)) {
	t.Helper()
	_, err := s.System.Check(ctx)
	verify("Check", err)
	_, err = s.System.Types(ctx)
	verify("Types", err)
	_, err = s.System.Find(ctx, "conformance-any")
	verify("Find", err)
	_, err = s.System.Create(ctx, taskmanager.New{Type: s.ticketType(), Title: "Conformance refused", Ref: ref("refused")})
	verify("Create", err)
	verify("Update", s.System.Update(ctx, key, taskmanager.Fields{Title: "x"}))
	_, err = s.System.Get(ctx, []string{key})
	verify("Get", err)
	if !s.lacks("Move") {
		_, err = s.System.Move(ctx, key, s.statuses()[1], nil)
		verify("Move", err)
	}
	if !s.lacks("Schedule") {
		verify("Schedule", s.System.Schedule(ctx, key, "conformance"))
	}
}

func rejectedCredential(t runT, s Subject) {
	tk := create(t, s, "unauthorized", taskmanager.New{})
	s.Controls.RejectCredential()
	defer s.Controls.Heal()
	everyOperation(t, s, tk.Key, func(op string, err error) {
		if !errors.Is(err, taskmanager.ErrUnauthorized) {
			t.Errorf("%s err = %v, want ErrUnauthorized", op, err)
		}
	})
}

func rateLimited(t runT, s Subject) {
	tk := create(t, s, "rate-limited", taskmanager.New{})
	reset := time.Now().Add(time.Minute).Truncate(time.Second)
	s.Controls.RateLimit(reset)
	defer s.Controls.Heal()
	everyOperation(t, s, tk.Key, func(op string, err error) {
		var rl *taskmanager.RateLimitError
		switch {
		case !errors.Is(err, taskmanager.ErrRateLimited) || !errors.As(err, &rl):
			t.Errorf("%s err = %v, want ErrRateLimited as a *RateLimitError", op, err)
		case rl.ResetAt.IsZero() || rl.ResetAt.Sub(reset).Abs() > 2*time.Second:
			t.Errorf("%s ResetAt = %v, want about %v", op, rl.ResetAt, reset)
		}
	})
}

func unavailable(t runT, s Subject) {
	tk := create(t, s, "unavailable", taskmanager.New{})
	s.Controls.Down()
	defer s.Controls.Heal()
	everyOperation(t, s, tk.Key, func(op string, err error) {
		if !errors.Is(err, taskmanager.ErrUnavailable) {
			t.Errorf("%s err = %v, want ErrUnavailable", op, err)
		}
	})
}
