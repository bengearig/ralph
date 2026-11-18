package tickets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

// Actions of a resolution.
const (
	ActionAccept    = "accept"
	ActionOverwrite = "overwrite"
)

var resolvableFields = []string{"title", "body", "status", driftDeleted}

// Change is one field a resolution settles: From is the value that is replaced, To the value that remains.
type Change struct{ Field, From, To string }

// ResolvePlan is what resolving the differences of one unit would do.
type ResolvePlan struct {
	Unit, Key, Action string
	Changes           []Change
}

// Fields names the fields the plan resolves.
func (p ResolvePlan) Fields() []string {
	out := make([]string, len(p.Changes))
	for i, c := range p.Changes {
		out[i] = c.Field
	}
	return out
}

// SplitBody is the inverse of how Build joins a body and its acceptance criteria. The last heading line wins.
func SplitBody(md string) (body, criteria string) {
	lines := strings.Split(md, "\n")
	at := -1
	for i, l := range lines {
		if strings.TrimRight(l, "\r") == criteriaHeading {
			at = i
		}
	}
	if at < 0 {
		return strings.Trim(md, "\r\n \t"), ""
	}
	return strings.TrimSpace(strings.Join(lines[:at], "\n")), strings.TrimSpace(strings.Join(lines[at+1:], "\n"))
}

func driftFields(row Row) []string {
	var out []string
	for _, f := range strings.Split(row.Drift, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// PlanResolve works out what accepting or overwriting the differences of a unit would change. It writes nothing.
// An empty fields means every field that differs.
func PlanResolve(ctx context.Context, d Deps, unitID, action string, fields []string) (ResolvePlan, error) {
	if action != ActionAccept && action != ActionOverwrite {
		return ResolvePlan{}, fmt.Errorf("tickets: unknown action %q", action)
	}
	u, err := d.Units.Get(ctx, unitID)
	if err != nil {
		return ResolvePlan{}, err
	}
	row, ok, err := NewStore(d.DB).Get(ctx, unitID)
	if err != nil {
		return ResolvePlan{}, err
	}
	if !ok {
		return ResolvePlan{}, fmt.Errorf("tickets: %s has no ticket", unitID)
	}
	links, err := d.Units.Links(ctx, unitID)
	if err != nil {
		return ResolvePlan{}, fmt.Errorf("tickets: links of %s: %w", unitID, err)
	}
	if row.LinkConfirmed && LinkKey(links) == "" {
		return ResolvePlan{}, fmt.Errorf("tickets: %s is detached from its ticket", unitID)
	}
	drifted := driftFields(row)
	if len(drifted) == 0 {
		return ResolvePlan{}, fmt.Errorf("%s has no differences to resolve", unitID)
	}
	if row.Origin == OriginIssue {
		for _, f := range fields {
			if f != driftDeleted {
				return ResolvePlan{}, fmt.Errorf("tickets: %s was started from %s, so only %q can be resolved, not %q", unitID, row.Key, driftDeleted, f)
			}
		}
	}
	for _, f := range fields {
		if !slices.Contains(resolvableFields, f) {
			return ResolvePlan{}, fmt.Errorf("tickets: unknown field %q", f)
		}
		if !slices.Contains(drifted, f) {
			return ResolvePlan{}, fmt.Errorf("tickets: field %q of %s has no difference", f, unitID)
		}
	}
	chosen := drifted
	if len(fields) > 0 {
		chosen = nil
		for _, f := range drifted {
			if slices.Contains(fields, f) {
				chosen = append(chosen, f)
			}
		}
	}

	var ticket *taskmanager.Ticket
	if !slices.Contains(chosen, driftDeleted) {
		found, err := d.System.Get(ctx, []string{row.Key})
		if err != nil {
			return ResolvePlan{}, fmt.Errorf("tickets: read tickets: %w", err)
		}
		t, ok := found[row.Key]
		if !ok {
			return ResolvePlan{}, fmt.Errorf("tickets: %s no longer exists; run ralph tickets check", row.Key)
		}
		ticket = &t
	}
	ralphBody := u.Body
	if u.AcceptanceCriteria != "" {
		ralphBody += "\n\n" + criteriaHeading + "\n\n" + u.AcceptanceCriteria
	}

	plan := ResolvePlan{Unit: unitID, Key: row.Key, Action: action}
	for _, f := range chosen {
		var c Change
		switch f {
		case "title":
			c = Change{Field: f, From: u.Title, To: ticket.Title}
		case "body":
			c = Change{Field: f, From: ralphBody, To: ticket.Body}
		case "status":
			c = Change{Field: f, From: row.PushedStatus, To: ticket.Status}
		case driftDeleted:
			c = Change{Field: f, From: row.Key}
			if action == ActionAccept {
				c.To = fmt.Sprintf("%s is gone from Jira; %s will be detached and no ticket made for it", row.Key, unitID)
			} else {
				c.To = fmt.Sprintf("%s is gone from Jira; a new ticket will be created for %s", row.Key, unitID)
			}
		}
		if action == ActionOverwrite && f != driftDeleted {
			c.From, c.To = c.To, c.From
		}
		plan.Changes = append(plan.Changes, c)
	}
	return plan, nil
}

// Resolve carries out a plan. An accept imports Jira's values into the unit; an overwrite clears what was pushed
// so a pass sends ralph's values, and runs that pass for the unit.
func Resolve(ctx context.Context, d Deps, p ResolvePlan) error {
	store := NewStore(d.DB)
	row, ok, err := store.Get(ctx, p.Unit)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tickets: %s has no ticket", p.Unit)
	}
	fields := p.Fields()
	switch p.Action {
	case ActionAccept:
		err = accept(ctx, d, store, row, p)
	case ActionOverwrite:
		err = overwrite(ctx, d, store, row, p)
	default:
		return fmt.Errorf("tickets: unknown action %q", p.Action)
	}
	if err != nil {
		return err
	}
	if p.Action == ActionAccept {
		_, err = d.Units.AddNote(ctx, p.Unit, units.NoteGeneral, fmt.Sprintf("Accepted from %s in Jira: %s", p.Key, strings.Join(fields, ", ")))
		return err
	}
	if _, err := d.Units.AddNote(ctx, p.Unit, units.NoteGeneral, fmt.Sprintf("Overwrote Jira's %s on %s with ralph's values", strings.Join(fields, ", "), p.Key)); err != nil {
		return err
	}
	_, err = Sync(ctx, d, SyncOptions{Units: []string{p.Unit}, Force: true})
	return err
}

func change(p ResolvePlan, field string) (Change, bool) {
	for _, c := range p.Changes {
		if c.Field == field {
			return c, true
		}
	}
	return Change{}, false
}

func settle(row *Row, p ResolvePlan) {
	var left []string
	for _, f := range driftFields(*row) {
		if _, resolved := change(p, f); !resolved {
			left = append(left, f)
		}
	}
	row.Drift = strings.Join(left, ",")
	if len(left) == 0 {
		row.DriftSeenAt = time.Time{}
	}
}

func accept(ctx context.Context, d Deps, store *Store, row Row, p ResolvePlan) error {
	title, hasTitle := change(p, "title")
	body, hasBody := change(p, "body")
	status, hasStatus := change(p, "status")
	_, hasDeleted := change(p, driftDeleted)

	if hasTitle || hasBody {
		u, err := d.Units.Get(ctx, p.Unit)
		if err != nil {
			return err
		}
		var c units.Change
		if hasTitle {
			c.Title = &title.To
		}
		if hasBody {
			text, criteria := SplitBody(body.To)
			c.Body = &text
			if u.Level == units.LevelTask || criteria != "" {
				c.AcceptanceCriteria = &criteria
			}
		}
		if _, err := d.Units.Update(ctx, p.Unit, u.Fingerprint, c); err != nil {
			return err
		}
		norm := d.System.Normalize(taskmanager.Fields{Title: title.To, Body: body.To})
		if hasTitle {
			row.PushedTitle = Hash(norm.Title)
		}
		if hasBody {
			row.PushedBody = Hash(norm.Body)
		}
	}
	if hasStatus {
		row.PushedStatus = status.To
		wanted, err := WantedStatus(ctx, d, p.Unit)
		if err != nil {
			return err
		}
		row.MappedStatus = wanted
	}
	if hasDeleted {
		err := d.Units.RemoveLink(ctx, p.Unit, units.LinkExternal, linkPrefix+row.Key)
		if err != nil && !errors.Is(err, units.ErrNotFound) {
			return err
		}
		row.LinkConfirmed = true
	}
	settle(&row, p)
	return store.Put(ctx, row)
}

func overwrite(ctx context.Context, d Deps, store *Store, row Row, p ResolvePlan) error {
	if _, ok := change(p, driftDeleted); ok {
		if err := d.Units.RemoveLink(ctx, p.Unit, units.LinkExternal, linkPrefix+row.Key); err != nil && !errors.Is(err, units.ErrNotFound) {
			return err
		}
		return store.Delete(ctx, p.Unit)
	}
	if _, ok := change(p, "title"); ok {
		row.PushedTitle = ""
	}
	if _, ok := change(p, "body"); ok {
		row.PushedBody = ""
	}
	if _, ok := change(p, "status"); ok {
		row.PushedStatus, row.MappedStatus = "", ""
	}
	settle(&row, p)
	return store.Put(ctx, row)
}
