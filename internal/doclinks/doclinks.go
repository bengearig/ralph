// Package doclinks links documents to units and reads those links in both
// directions. Only explicit links count: a document tied to a brief when it
// was created is not linked by that tie.
package doclinks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
)

// StatusMissing is the status of a link whose target is not a stored document.
const StatusMissing = "MISSING"

// Linked is a document linked to a unit. From is the ancestor it is inherited
// from and is empty for a direct link.
type Linked struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	SupersededBy string `json:"superseded_by,omitempty"`
	From         string `json:"from,omitempty"`
}

// UnitRef names a unit that a document is linked to.
type UnitRef struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	Title string `json:"title"`
}

type Service struct {
	units units.Service
	docs  documents.Service
}

func New(u units.Service, d documents.Service) *Service {
	return &Service{units: u, docs: d}
}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func (e *notFoundError) Unwrap() error { return documents.ErrNotFound }

func normalize(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func docNumber(id string) (int, bool) {
	digits, ok := strings.CutPrefix(id, "D-")
	if !ok || digits == "" || digits[0] == '0' {
		return 0, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

func checkDocID(id string) error {
	if _, ok := docNumber(id); !ok {
		return fmt.Errorf("%w: document ID %q must be D- followed by a number without leading zeros", units.ErrInvalid, id)
	}
	return nil
}

// Link links an ACTIVE document to a unit; linking twice changes nothing.
func (s *Service) Link(ctx context.Context, docID, unitID string) error {
	docID, unitID = normalize(docID), normalize(unitID)
	if err := checkDocID(docID); err != nil {
		return err
	}
	d, err := s.docs.Get(ctx, docID)
	if errors.Is(err, documents.ErrNotFound) {
		return &notFoundError{msg: fmt.Sprintf("document %s was not found", docID)}
	}
	if err != nil {
		return err
	}
	switch d.Status {
	case documents.StatusActive:
	case documents.StatusSuperseded:
		return fmt.Errorf("%w: document %s is superseded by %s and cannot be linked", units.ErrInvalid, docID, d.SupersededBy)
	default:
		return fmt.Errorf("%w: document %s is %s and cannot be linked", units.ErrInvalid, docID, strings.ToLower(string(d.Status)))
	}
	if _, err := s.units.Get(ctx, unitID); err != nil {
		return err
	}
	return s.units.AddLink(ctx, unitID, units.LinkDocument, docID)
}

// Unlink removes a link whatever the document's status, or whether it exists.
func (s *Service) Unlink(ctx context.Context, docID, unitID string) error {
	docID, unitID = normalize(docID), normalize(unitID)
	if err := checkDocID(docID); err != nil {
		return err
	}
	return s.units.RemoveLink(ctx, unitID, units.LinkDocument, docID)
}

// DocumentsOf returns the documents linked to a unit and those inherited from
// its ancestors, nearest ancestor first.
func (s *Service) DocumentsOf(ctx context.Context, unitID string) (direct, inherited []Linked, err error) {
	unitID = normalize(unitID)
	u, err := s.units.Get(ctx, unitID)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	direct, err = s.linked(ctx, u.ID, "", seen)
	if err != nil {
		return nil, nil, err
	}
	inherited = []Linked{}
	for parent := u.ParentID; parent != ""; {
		a, err := s.units.Get(ctx, parent)
		if err != nil {
			return nil, nil, err
		}
		more, err := s.linked(ctx, a.ID, a.ID, seen)
		if err != nil {
			return nil, nil, err
		}
		inherited = append(inherited, more...)
		parent = a.ParentID
	}
	return direct, inherited, nil
}

func (s *Service) linked(ctx context.Context, unitID, from string, seen map[string]bool) ([]Linked, error) {
	links, err := s.units.Links(ctx, unitID)
	if err != nil {
		return nil, err
	}
	type entry struct {
		n  int
		id string
	}
	var entries []entry
	for _, l := range links {
		if l.Kind != units.LinkDocument || seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		n, _ := docNumber(l.Target)
		entries = append(entries, entry{n, l.Target})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n < entries[j].n
		}
		return entries[i].id < entries[j].id
	})
	out := make([]Linked, 0, len(entries))
	for _, e := range entries {
		l := Linked{ID: e.id, Status: StatusMissing, From: from}
		if e.n != 0 {
			d, err := s.docs.Get(ctx, e.id)
			switch {
			case err == nil:
				l.Title, l.Kind, l.Status, l.SupersededBy = d.Title, string(d.Kind), string(d.Status), d.SupersededBy
			case errors.Is(err, documents.ErrNotFound):
			default:
				return nil, err
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// UnitsOf returns the units linked to a document, ordered by level then number.
func (s *Service) UnitsOf(ctx context.Context, docID string) ([]UnitRef, error) {
	docID = normalize(docID)
	if err := checkDocID(docID); err != nil {
		return nil, err
	}
	us, err := s.units.UnitsLinkedTo(ctx, units.LinkDocument, docID)
	if err != nil {
		return nil, err
	}
	out := make([]UnitRef, 0, len(us))
	for _, u := range us {
		out = append(out, UnitRef{ID: u.ID, Level: string(u.Level), Title: u.Title})
	}
	return out, nil
}
