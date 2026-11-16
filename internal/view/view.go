// Package view builds the status summary and the single-unit detail that
// `ralph status --json` and the MCP server both present.
package view

import (
	"context"
	"errors"
	"time"

	"github.com/bengearig/ralph/internal/units"
)

type Item struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type Slice struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Status string         `json:"status"`
	Tasks  map[string]int `json:"tasks"`
}

type Brief struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Slices []Slice `json:"slices"`
}

// SummaryView lists unfinished briefs with their slices, and tasks that have
// no slice. Hidden counts the finished items left out and is not serialized.
type SummaryView struct {
	Briefs []Brief `json:"briefs"`
	Tasks  []Item  `json:"tasks"`
	Hidden int     `json:"-"`
}

type Parent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Link struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type Note struct {
	Kind      string `json:"kind"`
	Author    string `json:"author,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type UnitView struct {
	ID                 string  `json:"id"`
	Level              string  `json:"level"`
	Title              string  `json:"title"`
	Status             string  `json:"status"`
	Parent             *Parent `json:"parent"`
	Body               string  `json:"body"`
	AcceptanceCriteria string  `json:"acceptance_criteria,omitempty"`
	CreatedAt          string  `json:"created_at"`
	CreatedBy          string  `json:"created_by,omitempty"`
	UpdatedAt          string  `json:"updated_at"`
	UpdatedBy          string  `json:"updated_by,omitempty"`
	Children           []Item  `json:"children"`
	Links              []Link  `json:"links"`
	Notes              []Note  `json:"notes"`
}

// NotFoundError reports a unit ID with no unit; it unwraps to units.ErrNotFound.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "unit " + e.ID + " was not found" }

func (e *NotFoundError) Unwrap() error { return units.ErrNotFound }

func finished(s units.Status) bool {
	return s == units.StatusDone || s == units.StatusCancelled
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Summary builds the status summary; finished work is left out unless all is set.
func Summary(ctx context.Context, svc units.Service, all bool) (SummaryView, error) {
	briefs, err := svc.List(ctx, units.Filter{Level: units.LevelBrief})
	if err != nil {
		return SummaryView{}, err
	}
	slices, err := svc.List(ctx, units.Filter{Level: units.LevelSlice})
	if err != nil {
		return SummaryView{}, err
	}
	tasks, err := svc.List(ctx, units.Filter{Level: units.LevelTask})
	if err != nil {
		return SummaryView{}, err
	}

	ids := make([]string, 0, len(briefs)+len(slices))
	for _, u := range briefs {
		ids = append(ids, u.ID)
	}
	for _, u := range slices {
		ids = append(ids, u.ID)
	}
	statuses, err := svc.Statuses(ctx, ids)
	if err != nil {
		return SummaryView{}, err
	}

	counts := map[string]map[string]int{}
	var orphans []units.Unit
	for _, t := range tasks {
		if t.ParentID == "" {
			orphans = append(orphans, t)
			continue
		}
		if counts[t.ParentID] == nil {
			counts[t.ParentID] = map[string]int{}
		}
		counts[t.ParentID][string(t.State)]++
	}
	slicesOf := map[string][]units.Unit{}
	for _, s := range slices {
		slicesOf[s.ParentID] = append(slicesOf[s.ParentID], s)
	}

	doc := SummaryView{Briefs: []Brief{}, Tasks: []Item{}}
	for _, b := range briefs {
		if !all && finished(statuses[b.ID]) {
			doc.Hidden++
			continue
		}
		bv := Brief{ID: b.ID, Title: b.Title, Status: string(statuses[b.ID]), Slices: []Slice{}}
		for _, s := range slicesOf[b.ID] {
			if !all && finished(statuses[s.ID]) {
				doc.Hidden++
				continue
			}
			tc := counts[s.ID]
			if tc == nil {
				tc = map[string]int{}
			}
			bv.Slices = append(bv.Slices, Slice{ID: s.ID, Title: s.Title, Status: string(statuses[s.ID]), Tasks: tc})
		}
		doc.Briefs = append(doc.Briefs, bv)
	}
	for _, t := range orphans {
		if !all && finished(units.Status(t.State)) {
			doc.Hidden++
			continue
		}
		doc.Tasks = append(doc.Tasks, Item{ID: t.ID, Title: t.Title, Status: string(t.State)})
	}
	return doc, nil
}

// UnitDetail builds one unit with its parent, children, links and notes.
// id must already be a normalized unit ID.
func UnitDetail(ctx context.Context, svc units.Service, id string) (UnitView, error) {
	u, err := svc.Get(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return UnitView{}, &NotFoundError{ID: id}
		}
		return UnitView{}, err
	}
	status, err := svc.Status(ctx, id)
	if err != nil {
		return UnitView{}, err
	}

	v := UnitView{
		ID: u.ID, Level: string(u.Level), Title: u.Title, Status: string(status),
		Body: u.Body, AcceptanceCriteria: u.AcceptanceCriteria,
		CreatedAt: stamp(u.CreatedAt), CreatedBy: u.CreatedBy,
		UpdatedAt: stamp(u.UpdatedAt), UpdatedBy: u.UpdatedBy,
		Children: []Item{}, Links: []Link{}, Notes: []Note{},
	}
	if u.ParentID != "" {
		p, err := svc.Get(ctx, u.ParentID)
		if err != nil {
			return UnitView{}, err
		}
		v.Parent = &Parent{ID: p.ID, Title: p.Title}
	}

	if u.Level != units.LevelTask {
		parent := u.ID
		kids, err := svc.List(ctx, units.Filter{ParentID: &parent})
		if err != nil {
			return UnitView{}, err
		}
		var slice []string
		for _, k := range kids {
			if k.Level != units.LevelTask {
				slice = append(slice, k.ID)
			}
		}
		derived, err := svc.Statuses(ctx, slice)
		if err != nil {
			return UnitView{}, err
		}
		for _, k := range kids {
			st := units.Status(k.State)
			if k.Level != units.LevelTask {
				st = derived[k.ID]
			}
			v.Children = append(v.Children, Item{ID: k.ID, Title: k.Title, Status: string(st)})
		}
	}

	links, err := svc.Links(ctx, id)
	if err != nil {
		return UnitView{}, err
	}
	for _, l := range links {
		v.Links = append(v.Links, Link{Kind: string(l.Kind), Target: l.Target})
	}
	notes, err := svc.Notes(ctx, id)
	if err != nil {
		return UnitView{}, err
	}
	for _, n := range notes {
		v.Notes = append(v.Notes, Note{Kind: string(n.Kind), Author: n.Author, Body: n.Body, CreatedAt: stamp(n.CreatedAt)})
	}
	return v, nil
}

func isNotFound(err error) bool { return errors.Is(err, units.ErrNotFound) }
