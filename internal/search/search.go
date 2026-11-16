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
// means all kinds and a zero Limit means the default.
type Query struct {
	Text  string
	Kinds []Kind
	Limit int
}

// Hit is one matching record. ID is the unit or document ID; for a note it is
// the ID of the unit the note is on. Status is a task's state or a document's
// status and is empty otherwise. A larger Score is a better match.
type Hit struct {
	Kind    Kind
	ID      string
	Title   string
	Snippet string
	Status  string
	Score   float64
	Unit    string
}

// Result is the outcome of a search. Mode names the kind of search that
// produced it; Relaxed is true when no hit matched every word and the hits
// match some of them.
type Result struct {
	Mode    string
	Relaxed bool
	Hits    []Hit
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
