package tickets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/filelock"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

// ErrSyncBusy means another ticket sync holds the lock.
var ErrSyncBusy = errors.New("another ticket sync is running")

const (
	linkPrefix        = "jira:"
	stateBlockedUntil = "blocked_until"
	unknownReset      = time.Minute

	stepCreate   = "create"
	stepSchedule = "schedule"
)

// SyncOptions choose what a pass covers.
type SyncOptions struct {
	// Units limits the pass to these units; empty means every mapped unit.
	Units []string
	// Force runs a pass that a rate limit has paused.
	Force bool
}

// Item is a unit a pass created, adopted, scheduled or skipped.
type Item struct{ Unit, Key, Reason string }

// Failure is a step that failed for a unit.
type Failure struct{ Unit, Step, Message string }

// Report is what a pass did.
type Report struct {
	Created, Adopted, Scheduled, Skipped []Item
	Failed                               []Failure
}

// Hash is how a pushed title or body is remembered.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type pass struct {
	d      Deps
	store  *Store
	byID   map[string]Row
	report Report
}

// Sync creates the tickets of the mapped units that have none. It returns the report of what was done even
// when it stops with an error.
func Sync(ctx context.Context, d Deps, opt SyncOptions) (Report, error) {
	lock, err := filelock.Acquire(filepath.Join(d.Dir, project.TicketsLockFile))
	if errors.Is(err, filelock.ErrHeld) {
		return Report{}, ErrSyncBusy
	}
	if err != nil {
		return Report{}, err
	}
	defer lock.Release()

	p := &pass{d: d, store: NewStore(d.DB)}
	if err := p.checkPaused(ctx, opt.Force); err != nil {
		return Report{}, err
	}
	rows, err := p.store.List(ctx)
	if err != nil {
		return Report{}, err
	}
	p.byID = make(map[string]Row, len(rows))
	for _, r := range rows {
		p.byID[r.UnitID] = r
	}
	for _, id := range opt.Units {
		if _, err := d.Units.Get(ctx, id); err != nil {
			return Report{}, err
		}
	}
	for _, level := range []units.Level{units.LevelBrief, units.LevelSlice, units.LevelTask} {
		if !d.Settings.Mapped(string(level)) {
			continue
		}
		list, err := d.Units.List(ctx, units.Filter{Level: level})
		if err != nil {
			return p.report, err
		}
		for _, u := range list {
			if len(opt.Units) > 0 && !slices.Contains(opt.Units, u.ID) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return p.report, err
			}
			if err := p.unit(ctx, u); err != nil {
				return p.report, err
			}
		}
	}
	return p.report, nil
}

func (p *pass) checkPaused(ctx context.Context, force bool) error {
	v, err := p.store.State(ctx, stateBlockedUntil)
	if err != nil || v == "" {
		return err
	}
	if until, perr := time.Parse(time.RFC3339, v); perr == nil && until.After(p.d.now()) && !force {
		return fmt.Errorf("ticket sync is paused until %s (rate limited)", until.UTC().Format(time.RFC3339))
	}
	return p.store.SetState(ctx, stateBlockedUntil, "")
}

// LinkKey returns the ticket key of the first jira link in links, empty when none.
func LinkKey(links []units.Link) string {
	for _, l := range links {
		if l.Kind == units.LinkExternal {
			if key, ok := strings.CutPrefix(l.Target, linkPrefix); ok && key != "" {
				return key
			}
		}
	}
	return ""
}

func (p *pass) unit(ctx context.Context, u units.Unit) error {
	links, err := p.d.Units.Links(ctx, u.ID)
	if err != nil {
		return p.fail(ctx, u, stepCreate, err)
	}
	linkKey := LinkKey(links)
	row, has := p.byID[u.ID]
	switch {
	case linkKey != "" && (!has || row.Key != linkKey):
		return p.adoptLink(ctx, u, linkKey)
	case has && !row.LinkConfirmed:
		if linkKey == "" {
			if err := p.d.Units.AddLink(ctx, u.ID, units.LinkExternal, linkPrefix+row.Key); err != nil {
				return p.fail(ctx, u, stepCreate, err)
			}
		}
		row.LinkConfirmed = true
		clearFailure(&row, stepCreate)
		if err := p.put(ctx, row); err != nil {
			return p.fail(ctx, u, stepCreate, err)
		}
	case has && linkKey == "":
		return nil
	case !has:
		if done, err := p.create(ctx, u); done || err != nil {
			return err
		}
	}
	return p.schedule(ctx, u)
}

func (p *pass) put(ctx context.Context, r Row) error {
	if err := p.store.Put(ctx, r); err != nil {
		return err
	}
	p.byID[r.UnitID] = r
	return nil
}

func (p *pass) adoptLink(ctx context.Context, u units.Unit, key string) error {
	row := Row{UnitID: u.ID, System: p.d.System.Name(), Key: key, LinkConfirmed: true, SyncedAt: p.d.now()}
	if err := p.put(ctx, row); err != nil {
		return p.fail(ctx, u, stepCreate, err)
	}
	p.adopted(u, key, "from its link")
	return nil
}

func (p *pass) adopted(u units.Unit, key, reason string) {
	p.report.Adopted = append(p.report.Adopted, Item{Unit: u.ID, Key: key, Reason: reason})
	p.d.emit(Event{Kind: "adopted", Unit: u.ID, Key: key, Message: reason})
}

// create handles a unit with neither a row nor a link. It reports done when nothing more is to be done for the
// unit in this pass.
func (p *pass) create(ctx context.Context, u units.Unit) (done bool, err error) {
	status, err := p.d.Units.Status(ctx, u.ID)
	if err != nil {
		return true, p.fail(ctx, u, stepCreate, err)
	}
	if status == units.StatusCancelled {
		p.skipped(u, "", "cancelled")
		return true, nil
	}
	found, err := p.d.System.Find(ctx, Ref(p.d.ProjectID, u.ID))
	if err != nil {
		return true, p.fail(ctx, u, stepCreate, err)
	}
	if found != nil {
		row := Row{UnitID: u.ID, System: p.d.System.Name(), Key: found.Key, ID: found.ID, SyncedAt: p.d.now()}
		if err := p.confirm(ctx, u, row); err != nil {
			return true, err
		}
		p.adopted(u, found.Key, "found by its reference")
		return true, nil
	}
	want, err := Build(ctx, p.d, u, p.byID)
	if errors.Is(err, ErrParentWaiting) {
		p.skipped(u, "", err.Error())
		return true, nil
	}
	if err != nil {
		return true, p.fail(ctx, u, stepCreate, err)
	}
	created, err := p.d.System.Create(ctx, taskmanager.New{Type: want.Type, Title: want.Title, Body: want.Body, Parent: want.Parent, Ref: want.Ref})
	if err != nil {
		return true, p.fail(ctx, u, stepCreate, err)
	}
	norm := p.d.System.Normalize(taskmanager.Fields{Title: want.Title, Body: want.Body})
	row := Row{
		UnitID: u.ID, System: p.d.System.Name(), Key: created.Key, ID: created.ID,
		PushedTitle: Hash(norm.Title), PushedBody: Hash(norm.Body), PushedStatus: created.Status, PushedParent: want.Parent,
		SyncedAt: p.d.now(),
	}
	if err := p.confirm(ctx, u, row); err != nil {
		return true, err
	}
	p.report.Created = append(p.report.Created, Item{Unit: u.ID, Key: created.Key})
	p.d.emit(Event{Kind: "created", Unit: u.ID, Key: created.Key})
	return false, nil
}

// confirm writes the row, then the link, then marks the link confirmed, so a crash between them is repaired by a
// later pass.
func (p *pass) confirm(ctx context.Context, u units.Unit, row Row) error {
	if err := p.put(ctx, row); err != nil {
		return p.fail(ctx, u, stepCreate, err)
	}
	if err := p.d.Units.AddLink(ctx, u.ID, units.LinkExternal, linkPrefix+row.Key); err != nil {
		return p.fail(ctx, u, stepCreate, err)
	}
	row.LinkConfirmed = true
	if err := p.put(ctx, row); err != nil {
		return p.fail(ctx, u, stepCreate, err)
	}
	return nil
}

func (p *pass) skipped(u units.Unit, key, reason string) {
	p.report.Skipped = append(p.report.Skipped, Item{Unit: u.ID, Key: key, Reason: reason})
	p.d.emit(Event{Kind: "skipped", Unit: u.ID, Key: key, Message: reason})
}

// schedule puts a ticket ralph created into the configured sprint. A row with no pushed title was adopted, and
// adopted tickets are never scheduled.
func (p *pass) schedule(ctx context.Context, u units.Unit) error {
	row, ok := p.byID[u.ID]
	sprint := p.d.Settings.Sprint
	if !ok || row.Scheduled || row.PushedTitle == "" || sprint == "" || !slices.Contains(p.d.Settings.SprintLevels, string(u.Level)) {
		return nil
	}
	if err := p.d.System.Schedule(ctx, row.Key, sprint); err != nil {
		return p.fail(ctx, u, stepSchedule, err)
	}
	row.Scheduled = true
	clearFailure(&row, stepSchedule)
	if err := p.put(ctx, row); err != nil {
		return p.fail(ctx, u, stepSchedule, err)
	}
	p.report.Scheduled = append(p.report.Scheduled, Item{Unit: u.ID, Key: row.Key, Reason: sprint})
	p.d.emit(Event{Kind: "scheduled", Unit: u.ID, Key: row.Key, Message: sprint})
	return nil
}

func clearFailure(r *Row, step string) {
	if r.FailedStep == step {
		r.FailedStep, r.FailedAt, r.LastError, r.Attempts = "", time.Time{}, "", 0
	}
}

// fail records a failed step and returns an error only when the pass must stop.
func (p *pass) fail(ctx context.Context, u units.Unit, step string, cause error) error {
	p.report.Failed = append(p.report.Failed, Failure{Unit: u.ID, Step: step, Message: cause.Error()})
	key := ""
	if row, ok := p.byID[u.ID]; ok {
		key = row.Key
		row.FailedStep, row.FailedAt, row.LastError, row.Attempts = step, p.d.now(), cause.Error(), row.Attempts+1
		if err := p.put(ctx, row); err != nil {
			return errors.Join(cause, err)
		}
	}
	p.d.emit(Event{Kind: "failed", Unit: u.ID, Key: key, Message: step + ": " + cause.Error()})
	switch {
	case errors.Is(cause, taskmanager.ErrUnauthorized):
		return cause
	case errors.Is(cause, taskmanager.ErrRateLimited):
		until := p.d.now().Add(unknownReset)
		var rl *taskmanager.RateLimitError
		if errors.As(cause, &rl) && !rl.ResetAt.IsZero() {
			until = rl.ResetAt
		}
		if err := p.store.SetState(ctx, stateBlockedUntil, until.UTC().Format(time.RFC3339)); err != nil {
			return errors.Join(cause, err)
		}
		return fmt.Errorf("ticket sync stopped: %w", cause)
	}
	return nil
}
