package taskmanagertest

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
)

type recorder struct {
	testing.TB
	results map[string]string
	name    string
	status  string
}

func newRecorder(t testing.TB) *recorder {
	return &recorder{TB: t, results: map[string]string{}, status: "pass"}
}

func (r *recorder) Run(name string, f func(runT)) bool {
	child := &recorder{TB: r.TB, results: r.results, name: name, status: "pass"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(child)
	}()
	<-done
	r.results[name] = child.status
	return child.status != "fail"
}

func (r *recorder) Name() string          { return r.name }
func (r *recorder) Fail()                 { r.status = "fail" }
func (r *recorder) FailNow()              { r.status = "fail"; runtime.Goexit() }
func (r *recorder) Failed() bool          { return r.status == "fail" }
func (r *recorder) Error(...any)          { r.status = "fail" }
func (r *recorder) Errorf(string, ...any) { r.status = "fail" }
func (r *recorder) Fatal(...any)          { r.FailNow() }
func (r *recorder) Fatalf(string, ...any) { r.FailNow() }
func (r *recorder) Skip(...any)           { r.SkipNow() }
func (r *recorder) Skipf(string, ...any)  { r.SkipNow() }
func (r *recorder) Skipped() bool         { return r.status == "skip" }
func (r *recorder) Log(...any)            {}
func (r *recorder) Logf(string, ...any)   {}
func (r *recorder) Helper()               {}
func (r *recorder) SkipNow() {
	if r.status != "fail" {
		r.status = "skip"
	}
	runtime.Goexit()
}

func fakeSubject() (*Fake, Subject) {
	f := NewFake("TEST")
	f.SetPlans("Plan 1")
	return f, Subject{System: f, Controls: f.Controls(), Plan: "Plan 1"}
}

func TestFakeConformance(t *testing.T) {
	_, s := fakeSubject()
	if m := s.MissingControls(); len(m) != 0 {
		t.Fatalf("missing controls: %v", m)
	}
	Run(t, s)
	r := newRecorder(t)
	run(r, s)
	for name, status := range r.results {
		if status != "pass" {
			t.Errorf("%s: %s", name, status)
		}
	}
	if len(r.results) != len(checks) {
		t.Errorf("ran %d checks, want %d", len(r.results), len(checks))
	}
}

func TestConformanceSkipsMissingControls(t *testing.T) {
	f := NewFake("TEST")
	s := Subject{System: f}
	r := newRecorder(t)
	run(r, s)
	for _, c := range checks {
		want := "pass"
		if len(c.needs) > 0 {
			want = "skip"
		}
		if got := r.results[c.name]; got != want {
			t.Errorf("%s = %s, want %s", c.name, got, want)
		}
	}
}

func TestFakeCallsRecorded(t *testing.T) {
	f := NewFake("TEST")
	tk, _ := f.Create(ctx, taskmanager.New{Type: "Task", Title: "t"})
	f.Normalize(taskmanager.Fields{})
	f.Name()
	_, _ = f.Get(ctx, []string{tk.Key})
	_, _ = f.Check(ctx)
	_ = f.Update(ctx, tk.Key, taskmanager.Fields{Title: "u"})
	want := []string{"Create", "Get", "Check", "Update"}
	if got := f.Calls(); !slices.Equal(got, want) {
		t.Errorf("Calls = %v, want %v", got, want)
	}
	f.Down()
	_, err := f.Types(ctx)
	if !errors.Is(err, taskmanager.ErrUnavailable) || f.Calls()[len(f.Calls())-1] != "Types" {
		t.Errorf("err = %v, calls = %v", err, f.Calls())
	}
}

func TestFakeNormalizeCutsTitle(t *testing.T) {
	f := NewFake("TEST")
	got := f.Normalize(taskmanager.Fields{Title: strings.Repeat("é", 300), Body: "text \n\n "})
	if n := len([]rune(got.Title)); n != 255 || got.Body != "text" {
		t.Errorf("Normalize = %d runes, body %q", n, got.Body)
	}
	if got := f.Normalize(taskmanager.Fields{Title: "short"}); got.Title != "short" {
		t.Errorf("short title = %q", got.Title)
	}
}

func TestFakeMoveFollowsFlow(t *testing.T) {
	f := NewFake("TEST")
	flow := []string{"To Do", "In Progress", "Done"}
	tk, _ := f.Create(ctx, taskmanager.New{Type: "Task", Title: "t"})
	m, err := f.Move(ctx, tk.Key, "Done", flow)
	if err != nil || m.From != "To Do" || m.To != "Done" || !slices.Equal(m.Via, []string{"In Progress"}) {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	f.ForceStatus(tk.Key, "To Do")
	if _, err := f.Move(ctx, tk.Key, "Done", nil); !errors.Is(err, taskmanager.ErrInvalid) || !strings.Contains(err.Error(), "In Progress") {
		t.Errorf("without flow err = %v", err)
	}
	f.SetWorkflow("Open", map[string][]string{"Open": {"Review"}, "Review": {"Closed"}, "Closed": {"Open"}})
	tk2, _ := f.Create(ctx, taskmanager.New{Type: "Task", Title: "t"})
	if tk2.Status != "Open" {
		t.Errorf("initial = %q", tk2.Status)
	}
	if m, err := f.Move(ctx, tk2.Key, "Closed", []string{"Open", "Review", "Closed"}); err != nil || !slices.Equal(m.Via, []string{"Review"}) {
		t.Errorf("Move = %+v, %v", m, err)
	}
	if m, err := f.Move(ctx, tk2.Key, "Open", nil); err != nil || m.To != "Open" {
		t.Errorf("Move back = %+v, %v", m, err)
	}
	f.SetWorkflow("Open", map[string][]string{"Open": {"Review"}})
	if _, err := f.Move(ctx, tk2.Key, "Closed", []string{"Open", "Review", "Closed"}); !errors.Is(err, taskmanager.ErrInvalid) {
		t.Errorf("broken flow err = %v", err)
	}
	if got := f.tickets[tk2.Key].Status; got != "Open" {
		t.Errorf("status after failed move = %q", got)
	}
}
