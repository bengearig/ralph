// Package documents stores architecture, design, decision and reference
// documents, project-wide or tied to one brief.
//
// Service is the interface callers depend on. Every implementation, local or
// remote, must return the package's sentinel errors (ErrNotFound and
// ErrInvalid) and db.ErrStale unchanged, so that errors.Is gives the same
// answer either way.
package documents

import (
	"context"
	"time"

	"github.com/bengearig/ralph/internal/db"
)

type Kind string

const (
	KindArchitecture Kind = "architecture"
	KindDesign       Kind = "design"
	KindDecision     Kind = "decision"
	KindReference    Kind = "reference"
)

func (k Kind) valid() bool {
	switch k {
	case KindArchitecture, KindDesign, KindDecision, KindReference:
		return true
	}
	return false
}

type Status string

const (
	StatusActive     Status = "ACTIVE"
	StatusArchived   Status = "ARCHIVED"
	StatusSuperseded Status = "SUPERSEDED"
)

func (s Status) valid() bool {
	switch s {
	case StatusActive, StatusArchived, StatusSuperseded:
		return true
	}
	return false
}

// Document is one stored record. BriefID is empty for a project-wide document
// and SupersededBy is empty unless the status is SUPERSEDED.
type Document struct {
	ID           string
	Number       int
	Kind         Kind
	BriefID      string
	Title        string
	Body         string
	Status       Status
	SupersededBy string
	Version      int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CreatedBy    string
	UpdatedBy    string
	Fingerprint  string
}

// NewDocument describes a document to create; an empty BriefID makes it project-wide.
type NewDocument struct {
	Kind    Kind
	BriefID string
	Title   string
	Body    string
}

// Change lists the fields Update sets; a nil field is left alone.
type Change struct {
	Title *string
	Body  *string
	Kind  *Kind
}

// Filter narrows List. A nil BriefID matches any scope and a pointer to the
// empty string matches project-wide documents only. Empty Statuses matches
// ACTIVE only and empty Kinds matches every kind.
type Filter struct {
	BriefID  *string
	Kinds    []Kind
	Statuses []Status
}

// VersionInfo describes one earlier version without its body; Size is the
// body's length in bytes.
type VersionInfo struct {
	Version   int
	Kind      Kind
	Title     string
	Size      int
	CreatedAt time.Time
	CreatedBy string
}

// DocumentVersion is the full content of one version. For the document's
// current version Current is true and the time and actor are those of its
// last edit.
type DocumentVersion struct {
	ID        string
	Version   int
	Current   bool
	Kind      Kind
	Title     string
	Body      string
	CreatedAt time.Time
	CreatedBy string
}

func fingerprint(d Document) string {
	return db.Fingerprint(d.ID, string(d.Kind), d.BriefID, d.Title, d.Body, string(d.Status), d.SupersededBy)
}

// Service is the document layer as callers see it.
type Service interface {
	// Create adds an ACTIVE document at version 1.
	Create(ctx context.Context, n NewDocument) (Document, error)
	// Get returns one document by ID.
	Get(ctx context.Context, id string) (Document, error)
	// List returns the matching documents ordered by number.
	List(ctx context.Context, f Filter) ([]Document, error)
	// Update applies a change to an ACTIVE document if its fingerprint is still expected.
	Update(ctx context.Context, id, expected string, c Change) (Document, error)
	// Archive moves an ACTIVE document to ARCHIVED if its fingerprint is still expected.
	Archive(ctx context.Context, id, expected string) (Document, error)
	// Supersede moves an ACTIVE document to SUPERSEDED, naming the ACTIVE
	// document that replaces it, if its fingerprint is still expected.
	Supersede(ctx context.Context, id, expected, supersededBy string) (Document, error)
	// Restore moves an ARCHIVED or SUPERSEDED document to ACTIVE if its
	// fingerprint is still expected.
	Restore(ctx context.Context, id, expected string) (Document, error)
	// Versions lists the earlier versions of a document of any status,
	// oldest first and without bodies.
	Versions(ctx context.Context, id string) ([]VersionInfo, error)
	// Version returns version n of a document of any status: the current
	// content when n is the current version, otherwise a kept earlier version.
	Version(ctx context.Context, id string, n int) (DocumentVersion, error)
	// Purge removes earlier versions chosen by opts when opts.Apply is set,
	// and otherwise reports what it would remove. It never touches a
	// document's current content.
	Purge(ctx context.Context, opts PurgeOptions) (PurgeResult, error)
}

var _ Service = (*Store)(nil)

// NewService returns the local implementation, for callers that should
// depend on the interface only.
func NewService(d *db.DB, opts ...Option) Service {
	return New(d, opts...)
}
