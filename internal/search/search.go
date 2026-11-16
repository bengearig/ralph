// Package search finds work records and documents by their text.
package search

import (
	"context"

	"github.com/bengearig/ralph/internal/db"
)

// Kind says what sort of record a hit is.
type Kind string

const (
	KindBrief    Kind = "brief"
	KindSlice    Kind = "slice"
	KindTask     Kind = "task"
	KindNote     Kind = "note"
	KindDocument Kind = "document"
)

func (k Kind) valid() bool {
	switch k {
	case KindBrief, KindSlice, KindTask, KindNote, KindDocument:
		return true
	}
	return false
}

// ModeKeyword is the Result.Mode of a keyword search.
const ModeKeyword = "keyword"

// Query is a search. Text is plain words, never index syntax; empty Kinds
// means all kinds and a zero Limit means the default. Archived and superseded
// documents, cancelled tasks and the notes on cancelled tasks are left out
// unless IncludeInactive is set.
type Query struct {
	Text            string
	Kinds           []Kind
	Limit           int
	IncludeInactive bool
}

// Hit is one matching record. ID is the unit or document ID; for a note it is
// the ID of the unit the note is on. Status is a task's state or a document's
// status and is empty otherwise. Inactive is true for a record that a search
// leaves out unless it includes inactive records; Status says why. A larger
// Score is a better match.
type Hit struct {
	Kind     Kind    `json:"kind" jsonschema:"brief, slice, task, note or document"`
	ID       string  `json:"id" jsonschema:"the unit or document ID; for a note, the unit it is on"`
	Title    string  `json:"title"`
	Snippet  string  `json:"snippet,omitempty" jsonschema:"an excerpt of the matching text with the matching words in square brackets"`
	Status   string  `json:"status,omitempty" jsonschema:"a task's state or a document's status"`
	Score    float64 `json:"score" jsonschema:"larger is a better match"`
	Unit     string  `json:"unit,omitempty"`
	Inactive bool    `json:"inactive" jsonschema:"true for an archived or superseded document, a cancelled task or a note on one"`
}

// Result is the outcome of a search. Mode names the kind of search that
// produced it; Relaxed is true when no hit matched every word and the hits
// match some of them.
type Result struct {
	Mode    string `json:"mode" jsonschema:"the kind of search that produced the result"`
	Relaxed bool   `json:"relaxed" jsonschema:"true when no record had every word and the hits match some of them"`
	Hits    []Hit  `json:"hits"`
}

// Service is search as callers see it. Implementations, local or remote,
// return these types and errors.
type Service interface {
	// Search returns the records matching a query, best first.
	Search(ctx context.Context, q Query) (Result, error)
}

// New returns the keyword search over the project database.
func New(d *db.DB) Service {
	return &keyword{db: d}
}
