// Package taskmanagertest holds an in-memory taskmanager.System and the
// conformance suite every system must pass.
package taskmanagertest

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
)

const maxTitle = 255

// Fake is an in-memory taskmanager.System.
type Fake struct {
	mu          sync.Mutex
	project     string
	next        int
	tickets     map[string]taskmanager.Ticket
	initial     string
	transitions map[string][]string
	plans       map[string][]string
	known       map[string]bool
	calls       []string

	rejected bool
	limitAt  time.Time
	limited  bool
	down     bool
}

var _ taskmanager.System = (*Fake)(nil)

// NewFake returns a fake whose ticket keys are <project>-<n>, with the
// workflow To Do, In Progress and Done.
func NewFake(project string) *Fake {
	f := &Fake{
		project: project,
		tickets: map[string]taskmanager.Ticket{},
		plans:   map[string][]string{},
		known:   map[string]bool{},
	}
	f.SetWorkflow("To Do", map[string][]string{
		"To Do":       {"In Progress"},
		"In Progress": {"Done"},
		"Done":        {"In Progress"},
	})
	return f
}

// SetWorkflow sets the status new tickets start in and the statuses each
// status has a direct transition to.
func (f *Fake) SetWorkflow(initial string, transitions map[string][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initial = initial
	f.transitions = map[string][]string{}
	for from, to := range transitions {
		f.transitions[from] = slices.Clone(to)
	}
}

// SetPlans sets the plan names Schedule accepts.
func (f *Fake) SetPlans(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known = map[string]bool{}
	for _, n := range names {
		f.known[n] = true
	}
}

// Scheduled returns the plans the ticket was put in, in order.
func (f *Fake) Scheduled(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.plans[key])
}

// RejectCredential makes every call fail as an unauthorized one.
func (f *Fake) RejectCredential() { f.set(func() { f.rejected = true }) }

// RateLimit makes every call fail as a rate-limited one that resets at until.
func (f *Fake) RateLimit(until time.Time) {
	f.set(func() { f.limited, f.limitAt = true, until })
}

// Down makes every call fail as an unavailable one.
func (f *Fake) Down() { f.set(func() { f.down = true }) }

// Heal undoes RejectCredential, RateLimit and Down.
func (f *Fake) Heal() {
	f.set(func() { f.rejected, f.limited, f.down, f.limitAt = false, false, false, time.Time{} })
}

// Delete removes a ticket as a person in the system would.
func (f *Fake) Delete(key string) {
	f.set(func() { delete(f.tickets, key) })
}

// Edit changes a ticket as a person in the system would.
func (f *Fake) Edit(key string, fields taskmanager.Fields) {
	f.set(func() {
		if t, ok := f.tickets[key]; ok {
			n := f.Normalize(fields)
			t.Title, t.Body = n.Title, n.Body
			f.tickets[key] = t
		}
	})
}

// ForceStatus puts a ticket in a status without following the workflow.
func (f *Fake) ForceStatus(key, status string) {
	f.set(func() {
		if t, ok := f.tickets[key]; ok {
			t.Status = status
			f.tickets[key] = t
		}
	})
}

// Controls returns the fake's controls for a conformance Subject.
func (f *Fake) Controls() Controls {
	return Controls{
		RejectCredential: f.RejectCredential,
		RateLimit:        f.RateLimit,
		Down:             f.Down,
		Heal:             f.Heal,
		Delete:           f.Delete,
		Edit:             f.Edit,
		ForceStatus:      f.ForceStatus,
	}
}

// Calls returns the names of the methods called that reach the system, in
// order: every method but Name and Normalize.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Fake) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// enter records a call and returns the error the system is set to give. The
// caller holds no lock; enter takes it and the caller must unlock.
func (f *Fake) enter(method string) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	switch {
	case f.rejected:
		return taskmanager.NewCredentialError("fake.token", "the fake rejected the credentials")
	case f.limited:
		return &taskmanager.RateLimitError{ResetAt: f.limitAt}
	case f.down:
		return fmt.Errorf("fake: %w", taskmanager.ErrUnavailable)
	}
	return nil
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Check(context.Context) (taskmanager.Account, error) {
	defer f.mu.Unlock()
	if err := f.enter("Check"); err != nil {
		return taskmanager.Account{}, err
	}
	return taskmanager.Account{Name: "Fake User", Email: "fake@example.com"}, nil
}

func (f *Fake) Types(context.Context) ([]string, error) {
	defer f.mu.Unlock()
	if err := f.enter("Types"); err != nil {
		return nil, err
	}
	return []string{"Feature", "Task", "Subtask"}, nil
}

func (f *Fake) Find(_ context.Context, ref string) (*taskmanager.Ticket, error) {
	defer f.mu.Unlock()
	if err := f.enter("Find"); err != nil {
		return nil, err
	}
	for _, k := range f.sortedKeys() {
		if t := f.tickets[k]; ref != "" && t.Ref == ref {
			return &t, nil
		}
	}
	return nil, nil
}

func (f *Fake) Create(_ context.Context, n taskmanager.New) (taskmanager.Ticket, error) {
	defer f.mu.Unlock()
	if err := f.enter("Create"); err != nil {
		return taskmanager.Ticket{}, err
	}
	if n.Type == "" || n.Title == "" {
		return taskmanager.Ticket{}, fmt.Errorf("a ticket needs a type and a title: %w", taskmanager.ErrInvalid)
	}
	if n.Parent != "" {
		if _, ok := f.tickets[n.Parent]; !ok {
			return taskmanager.Ticket{}, fmt.Errorf("parent %s does not exist: %w", n.Parent, taskmanager.ErrInvalid)
		}
	}
	f.next++
	norm := f.Normalize(taskmanager.Fields{Title: n.Title, Body: n.Body})
	t := taskmanager.Ticket{
		Key:    fmt.Sprintf("%s-%d", f.project, f.next),
		ID:     strconv.Itoa(10000 + f.next),
		Type:   n.Type,
		Title:  norm.Title,
		Body:   norm.Body,
		Status: f.initial,
		Parent: n.Parent,
		Ref:    n.Ref,
	}
	f.tickets[t.Key] = t
	return t, nil
}

func (f *Fake) Update(_ context.Context, key string, fields taskmanager.Fields) error {
	defer f.mu.Unlock()
	if err := f.enter("Update"); err != nil {
		return err
	}
	t, ok := f.tickets[key]
	if !ok {
		return fmt.Errorf("ticket %s: %w", key, taskmanager.ErrNotFound)
	}
	n := f.Normalize(fields)
	t.Title, t.Body = n.Title, n.Body
	f.tickets[key] = t
	return nil
}

func (f *Fake) Get(_ context.Context, keys []string) (map[string]taskmanager.Ticket, error) {
	defer f.mu.Unlock()
	if err := f.enter("Get"); err != nil {
		return nil, err
	}
	out := map[string]taskmanager.Ticket{}
	for _, k := range keys {
		if t, ok := f.tickets[k]; ok {
			out[k] = t
		}
	}
	return out, nil
}

func (f *Fake) Move(_ context.Context, key, status string, flow []string) (taskmanager.Move, error) {
	defer f.mu.Unlock()
	if err := f.enter("Move"); err != nil {
		return taskmanager.Move{}, err
	}
	t, ok := f.tickets[key]
	if !ok {
		return taskmanager.Move{}, fmt.Errorf("ticket %s: %w", key, taskmanager.ErrNotFound)
	}
	from := t.Status
	if from == status {
		return taskmanager.Move{From: from, To: status}, nil
	}
	path := f.path(from, status, flow)
	if path == nil {
		return taskmanager.Move{}, fmt.Errorf("cannot move %s from %q to %q; reachable from %q: %s: %w",
			key, from, status, from, strings.Join(f.transitions[from], ", "), taskmanager.ErrInvalid)
	}
	cur := from
	var via []string
	for _, next := range path {
		if !slices.Contains(f.transitions[cur], next) {
			return taskmanager.Move{}, fmt.Errorf("cannot move %s from %q to %q; reachable from %q: %s: %w",
				key, cur, next, cur, strings.Join(f.transitions[cur], ", "), taskmanager.ErrInvalid)
		}
		if cur != from {
			via = append(via, cur)
		}
		cur = next
	}
	t.Status = status
	f.tickets[key] = t
	return taskmanager.Move{From: from, To: status, Via: via}, nil
}

// path returns the statuses to step to: the target alone when a transition
// leads straight to it, otherwise the stretch of flow after from up to and
// including the target, or nil when flow does not hold both.
func (f *Fake) path(from, to string, flow []string) []string {
	if slices.Contains(f.transitions[from], to) {
		return []string{to}
	}
	i, j := slices.Index(flow, from), slices.Index(flow, to)
	switch {
	case i < 0 || j < 0:
		return nil
	case i < j:
		return slices.Clone(flow[i+1 : j+1])
	default:
		out := slices.Clone(flow[j:i])
		slices.Reverse(out)
		return out
	}
}

func (f *Fake) Schedule(_ context.Context, key, plan string) error {
	defer f.mu.Unlock()
	if err := f.enter("Schedule"); err != nil {
		return err
	}
	if _, ok := f.tickets[key]; !ok {
		return fmt.Errorf("ticket %s: %w", key, taskmanager.ErrNotFound)
	}
	if !f.known[plan] {
		return fmt.Errorf("plan %q: %w", plan, taskmanager.ErrNotFound)
	}
	f.plans[key] = append(f.plans[key], plan)
	return nil
}

// Normalize cuts a title at 255 characters and trims the trailing space of a
// body.
func (f *Fake) Normalize(fields taskmanager.Fields) taskmanager.Fields {
	if r := []rune(fields.Title); len(r) > maxTitle {
		fields.Title = string(r[:maxTitle])
	}
	fields.Body = strings.TrimRightFunc(fields.Body, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	return fields
}

func (f *Fake) sortedKeys() []string {
	keys := make([]string, 0, len(f.tickets))
	for k := range f.tickets {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		na, _ := strconv.Atoi(a[strings.LastIndex(a, "-")+1:])
		nb, _ := strconv.Atoi(b[strings.LastIndex(b, "-")+1:])
		return na - nb
	})
	return keys
}
