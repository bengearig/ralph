package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const documentsText = `A document is a piece of the project's written design that you and the user keep in ralph: its kind is one of architecture, design, decision or reference. A document is project-wide, or tied to one brief (brief_id, such as B-1); the scope is chosen when it is created and never changes. Its status is ACTIVE (in use), ARCHIVED (no longer in use) or SUPERSEDED (replaced: it names its replacement in superseded_by, and a reader follows that pointer). Only an ACTIVE document can be edited; restore an archived or superseded one first. Every edit that changes something keeps the earlier version, which doc_versions lists and doc_get reads.`

const docFingerprintText = `Every document carries a fingerprint that changes whenever the document changes. Writes to an existing document need the fingerprint from your latest read of it (doc_get, doc_list or the result of an earlier write); if another writer changed the document since, the call fails with error_code "stale" and you must call doc_get and retry.`

var (
	docKindEnum   = []any{"architecture", "design", "decision", "reference"}
	docStatusEnum = []any{"ACTIVE", "ARCHIVED", "SUPERSEDED"}
)

// documentError marks an error raised by a document tool so that a stale
// fingerprint names doc_get rather than unit_get.
type documentError struct{ err error }

func (e *documentError) Error() string { return e.err.Error() }

func (e *documentError) Unwrap() error { return e.err }

func isDocumentStale(err error) bool {
	var de *documentError
	return errors.As(err, &de) && errors.Is(err, db.ErrStale)
}

// docResult is a document with the fingerprint to pass on the next write.
type docResult struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	BriefID      string `json:"brief_id" jsonschema:"the brief the document is tied to; empty for a project-wide document"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Status       string `json:"status"`
	SupersededBy string `json:"superseded_by" jsonschema:"the replacement's ID when the status is SUPERSEDED; otherwise empty"`
	Version      int    `json:"version" jsonschema:"the current version number"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
	UpdatedAt    string `json:"updated_at"`
	UpdatedBy    string `json:"updated_by"`
	Fingerprint  string `json:"fingerprint"`
}

// docGetResult is either a document (with its fingerprint) or, when a version
// was asked for, that version (with no fingerprint).
type docGetResult struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Version      int    `json:"version"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
	BriefID      string `json:"brief_id,omitempty"`
	Status       string `json:"status,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	UpdatedBy    string `json:"updated_by,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	Current      *bool  `json:"current,omitempty" jsonschema:"only when a version was asked for: whether it is the document's current version"`

	LinkedUnits []doclinks.UnitRef `json:"linked_units" jsonschema:"the units explicitly linked to the document (id, level, title); empty when none"`
}

type docSummary struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	BriefID      string `json:"brief_id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	SupersededBy string `json:"superseded_by"`
	Version      int    `json:"version"`
	UpdatedAt    string `json:"updated_at"`
	Fingerprint  string `json:"fingerprint"`
}

type docListResult struct {
	Documents []docSummary `json:"documents"`
}

type docVersionSummary struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Size      int    `json:"size" jsonschema:"length of the body in bytes"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

type docVersionsResult struct {
	ID       string              `json:"id"`
	Current  int                 `json:"current" jsonschema:"the current version number, which is not in versions"`
	Versions []docVersionSummary `json:"versions" jsonschema:"earlier versions, oldest first, without bodies"`
}

type docCreateInput struct {
	Kind    string `json:"kind" jsonschema:"architecture, design, decision or reference"`
	Title   string `json:"title" jsonschema:"one-line title of at most 200 characters"`
	Body    string `json:"body" jsonschema:"the document text in markdown, not blank"`
	BriefID string `json:"brief_id,omitempty" jsonschema:"brief ID such as B-1 to tie the document to; omit for a project-wide document"`
}

type docGetInput struct {
	ID      string `json:"id" jsonschema:"document ID such as D-1"`
	Version *int   `json:"version,omitempty" jsonschema:"read this version (from 1) instead of the document; omit for the document itself"`
}

type docListInput struct {
	BriefID     string   `json:"brief_id,omitempty" jsonschema:"only documents tied to this brief; cannot be combined with project_only"`
	ProjectOnly bool     `json:"project_only,omitempty" jsonschema:"only project-wide documents; cannot be combined with brief_id"`
	Kinds       []string `json:"kinds,omitempty" jsonschema:"only documents of these kinds"`
	Statuses    []string `json:"statuses,omitempty" jsonschema:"only documents with these statuses; omit for ACTIVE documents only"`
}

type docUpdateInput struct {
	ID          string  `json:"id" jsonschema:"document ID such as D-1"`
	Fingerprint string  `json:"fingerprint" jsonschema:"fingerprint from your latest read of this document"`
	Title       *string `json:"title,omitempty" jsonschema:"new title; omit to keep"`
	Body        *string `json:"body,omitempty" jsonschema:"new body in markdown; omit to keep"`
	Kind        *string `json:"kind,omitempty" jsonschema:"new kind; omit to keep"`
}

type docWriteInput struct {
	ID          string `json:"id" jsonschema:"document ID such as D-1"`
	Fingerprint string `json:"fingerprint" jsonschema:"fingerprint from your latest read of this document"`
}

type docSupersedeInput struct {
	ID           string `json:"id" jsonschema:"the document being replaced, such as D-1"`
	Fingerprint  string `json:"fingerprint" jsonschema:"fingerprint from your latest read of this document"`
	SupersededBy string `json:"superseded_by" jsonschema:"ID of the ACTIVE document that replaces it; not the document itself"`
}

type docIDInput struct {
	ID string `json:"id" jsonschema:"document ID such as D-1"`
}

type docHandle[In any] func(ctx context.Context, svc documents.Service, in In) (out any, text string, err error)

// documents returns the kept documents service, building it if this is the
// first call or every earlier attempt failed.
func (s *server) documents(ctx context.Context) (documents.Service, error) {
	s.docMu.Lock()
	defer s.docMu.Unlock()
	if s.docs != nil {
		return s.docs, nil
	}
	svc, err := s.opts.Documents(ctx)
	if err != nil {
		return nil, &openError{err}
	}
	s.docs = svc
	return svc, nil
}

func addDocTool[In any](s *server, srv *mcp.Server, t *mcp.Tool, fn docHandle[In]) {
	if sc, ok := t.OutputSchema.(*jsonschema.Schema); ok {
		withNoticesSchema(sc)
	}
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx = units.WithActor(ctx, s.opts.Actor)
		res, out := callDocTool(ctx, s, fn, in)
		return s.deliver(ctx, res, out)
	})
}

func callDocTool[In any](ctx context.Context, s *server, fn docHandle[In], in In) (*mcp.CallToolResult, any) {
	svc, err := s.documents(ctx)
	if err != nil {
		return errorResult(err), nil
	}
	out, text, err := fn(ctx, svc, in)
	if err != nil {
		return errorResult(&documentError{err}), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out
}

func (s *server) addDocTools(srv *mcp.Server) {
	if s.opts.Documents == nil {
		return
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	writes := func(idempotent bool) *mcp.ToolAnnotations {
		no := false
		return &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: idempotent}
	}
	kindSchema := func(p map[string]*jsonschema.Schema) { p["kind"] = enum(docKindEnum) }

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_create",
		Description: `Create a document and return it with its fingerprint. It starts ACTIVE at version 1. kind is architecture, design, decision or reference; title is one line; body is markdown and not blank. Give brief_id to tie the document to one brief, or omit it for a project-wide document; the scope cannot be changed afterwards, and a slice or task is not a brief (error_code "invalid").

` + documentsText + `

` + docFingerprintText,
		Annotations:  writes(false),
		InputSchema:  schemaFor[docCreateInput](kindSchema),
		OutputSchema: outputFor[docResult](),
	}, func(ctx context.Context, svc documents.Service, in docCreateInput) (any, string, error) {
		d, err := svc.Create(ctx, documents.NewDocument{
			Kind: documents.Kind(in.Kind), BriefID: normalizeID(in.BriefID), Title: in.Title, Body: in.Body,
		})
		if err != nil {
			return nil, "", err
		}
		return docOut(d)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_get",
		Description: `Read one document: its kind, brief_id (empty when project-wide), title, body, status, superseded_by, version, who created and last changed it, its fingerprint and linked_units (the briefs, slices and tasks linked to it, each with id, level and title; empty when none). Call this before changing a document, and again after a "stale" error. A SUPERSEDED document names its replacement in superseded_by: read that one instead.

With version, read that version of the document instead: the earlier or the current one, as id, version, current (whether it is the current version), kind, title, body, created_at and created_by. A version has no fingerprint; use the document itself for writes.

` + documentsText + `

` + docFingerprintText,
		Annotations:  readOnly,
		InputSchema:  schemaFor[docGetInput](nil),
		OutputSchema: outputFor[docGetResult](),
	}, func(ctx context.Context, svc documents.Service, in docGetInput) (any, string, error) {
		id := normalizeID(in.ID)
		if in.Version != nil {
			v, err := svc.Version(ctx, id, *in.Version)
			if err != nil {
				return nil, "", err
			}
			linked, err := s.linkedUnits(ctx, svc, id)
			if err != nil {
				return nil, "", err
			}
			cur := v.Current
			out := docGetResult{
				ID: v.ID, Kind: string(v.Kind), Title: v.Title, Body: v.Body, Version: v.Version,
				CreatedAt: formatTime(v.CreatedAt), CreatedBy: v.CreatedBy, Current: &cur, LinkedUnits: linked,
			}
			label := "version"
			if cur {
				label = "current version"
			}
			return out, fmt.Sprintf("%s %s %d (%s) %s\n\n%s", v.ID, label, v.Version, v.Kind, v.Title, v.Body), nil
		}
		d, err := svc.Get(ctx, id)
		if err != nil {
			return nil, "", err
		}
		linked, err := s.linkedUnits(ctx, svc, id)
		if err != nil {
			return nil, "", err
		}
		out := docGetResult{
			ID: d.ID, Kind: string(d.Kind), Title: d.Title, Body: d.Body, Version: d.Version,
			CreatedAt: formatTime(d.CreatedAt), CreatedBy: d.CreatedBy, BriefID: d.BriefID,
			Status: string(d.Status), SupersededBy: d.SupersededBy,
			UpdatedAt: formatTime(d.UpdatedAt), UpdatedBy: d.UpdatedBy, Fingerprint: d.Fingerprint,
			LinkedUnits: linked,
		}
		return out, docText(d, linked), nil
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_list",
		Description: `List document summaries (id, kind, brief_id, title, status, superseded_by, version, updated_at, fingerprint) without bodies, in the order they were created. Only ACTIVE documents are listed unless statuses is given. All filters are optional and combine: brief_id (documents tied to that brief), project_only (project-wide documents; cannot be combined with brief_id), kinds and statuses. Read a document with doc_get.

` + documentsText,
		Annotations: readOnly,
		InputSchema: schemaFor[docListInput](func(p map[string]*jsonschema.Schema) {
			p["kinds"].Items = enum(docKindEnum)
			p["statuses"].Items = enum(docStatusEnum)
		}),
		OutputSchema: outputFor[docListResult](),
	}, func(ctx context.Context, svc documents.Service, in docListInput) (any, string, error) {
		return listDocs(ctx, svc, in)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_update",
		Description: `Change a document's title, body or kind; only the fields you give change. Returns the document with its new fingerprint. Only an ACTIVE document can be edited: restore an archived or superseded one with doc_restore first (error_code "invalid" otherwise). A change that alters nothing keeps the version as it is. An edit that changes something keeps the earlier content as an earlier version. The scope cannot be changed. To archive, supersede or restore use doc_archive, doc_supersede and doc_restore.

` + documentsText + `

` + docFingerprintText,
		Annotations:  writes(true),
		InputSchema:  schemaFor[docUpdateInput](kindSchema),
		OutputSchema: outputFor[docResult](),
	}, func(ctx context.Context, svc documents.Service, in docUpdateInput) (any, string, error) {
		c := documents.Change{Title: in.Title, Body: in.Body}
		if in.Kind != nil {
			k := documents.Kind(*in.Kind)
			c.Kind = &k
		}
		d, err := svc.Update(ctx, normalizeID(in.ID), in.Fingerprint, c)
		if err != nil {
			return nil, "", err
		}
		return docOut(d)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_archive",
		Description: `Mark an ACTIVE document ARCHIVED: it is no longer in use. Archived documents are left out of doc_list unless statuses asks for them, and cannot be edited until restored with doc_restore. Returns the document. Use doc_supersede instead when another document replaces it.

` + documentsText + `

` + docFingerprintText,
		Annotations:  writes(false),
		InputSchema:  schemaFor[docWriteInput](nil),
		OutputSchema: outputFor[docResult](),
	}, func(ctx context.Context, svc documents.Service, in docWriteInput) (any, string, error) {
		d, err := svc.Archive(ctx, normalizeID(in.ID), in.Fingerprint)
		if err != nil {
			return nil, "", err
		}
		return docOut(d)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_supersede",
		Description: `Mark an ACTIVE document SUPERSEDED because another document replaces it. superseded_by is required: the ID of an existing ACTIVE document other than this one. The superseded document names its replacement in superseded_by, so a reader can follow it; there is no superseding without a replacement. Superseded documents are left out of doc_list unless statuses asks for them, and cannot be edited until restored with doc_restore. Returns the document.

` + documentsText + `

` + docFingerprintText,
		Annotations:  writes(false),
		InputSchema:  schemaFor[docSupersedeInput](nil),
		OutputSchema: outputFor[docResult](),
	}, func(ctx context.Context, svc documents.Service, in docSupersedeInput) (any, string, error) {
		d, err := svc.Supersede(ctx, normalizeID(in.ID), in.Fingerprint, normalizeID(in.SupersededBy))
		if err != nil {
			return nil, "", err
		}
		return docOut(d)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_restore",
		Description: `Make an ARCHIVED or SUPERSEDED document ACTIVE again, clearing its superseded_by pointer, so that it can be edited and appears in doc_list. Returns the document.

` + documentsText + `

` + docFingerprintText,
		Annotations:  writes(false),
		InputSchema:  schemaFor[docWriteInput](nil),
		OutputSchema: outputFor[docResult](),
	}, func(ctx context.Context, svc documents.Service, in docWriteInput) (any, string, error) {
		d, err := svc.Restore(ctx, normalizeID(in.ID), in.Fingerprint)
		if err != nil {
			return nil, "", err
		}
		return docOut(d)
	})

	addDocTool(s, srv, &mcp.Tool{
		Name: "doc_versions",
		Description: `List the earlier versions of a document, of any status, oldest first: version, kind, title, size (body length in bytes), created_at and created_by, without bodies. The result also gives the current version number, which is not in the list. Read the content of one with doc_get and its version. A person may have purged old versions, so numbers can be missing.

` + documentsText,
		Annotations:  readOnly,
		InputSchema:  schemaFor[docIDInput](nil),
		OutputSchema: outputFor[docVersionsResult](),
	}, func(ctx context.Context, svc documents.Service, in docIDInput) (any, string, error) {
		id := normalizeID(in.ID)
		d, err := svc.Get(ctx, id)
		if err != nil {
			return nil, "", err
		}
		list, err := svc.Versions(ctx, id)
		if err != nil {
			return nil, "", err
		}
		out := docVersionsResult{ID: id, Current: d.Version, Versions: []docVersionSummary{}}
		var b strings.Builder
		fmt.Fprintf(&b, "%s is at version %d.", id, d.Version)
		for _, v := range list {
			out.Versions = append(out.Versions, docVersionSummary{
				Version: v.Version, Kind: string(v.Kind), Title: v.Title, Size: v.Size,
				CreatedAt: formatTime(v.CreatedAt), CreatedBy: v.CreatedBy,
			})
			fmt.Fprintf(&b, "\nversion %d %s %s (%d bytes, %s %s)", v.Version, v.Kind, v.Title, v.Size, formatTime(v.CreatedAt), v.CreatedBy)
		}
		if len(list) == 0 {
			b.WriteString(" No earlier versions are kept.")
		}
		return out, b.String(), nil
	})
}

func normalizeID(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func docOut(d documents.Document) (any, string, error) {
	out := docResult{
		ID: d.ID, Kind: string(d.Kind), BriefID: d.BriefID, Title: d.Title, Body: d.Body,
		Status: string(d.Status), SupersededBy: d.SupersededBy, Version: d.Version,
		CreatedAt: formatTime(d.CreatedAt), CreatedBy: d.CreatedBy,
		UpdatedAt: formatTime(d.UpdatedAt), UpdatedBy: d.UpdatedBy, Fingerprint: d.Fingerprint,
	}
	return out, docText(d, nil), nil
}

// linkedUnits lists the units linked to a document, with the empty array
// rather than nil when there are none.
func (s *server) linkedUnits(ctx context.Context, docs documents.Service, id string) ([]doclinks.UnitRef, error) {
	if s.opts.Open == nil {
		return []doclinks.UnitRef{}, nil
	}
	svc, err := s.service(ctx)
	if err != nil {
		return nil, err
	}
	return doclinks.New(svc, docs).UnitsOf(ctx, id)
}

func docText(d documents.Document, linked []doclinks.UnitRef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s\nfingerprint: %s\nversion: %d\n", d.ID, d.Kind, d.Status, d.Title, d.Fingerprint, d.Version)
	if d.BriefID != "" {
		fmt.Fprintf(&b, "brief: %s\n", d.BriefID)
	}
	if d.SupersededBy != "" {
		fmt.Fprintf(&b, "superseded by: %s\n", d.SupersededBy)
	}
	for _, u := range linked {
		fmt.Fprintf(&b, "linked: %s %s %s\n", u.ID, u.Level, u.Title)
	}
	fmt.Fprintf(&b, "\n%s", d.Body)
	return b.String()
}

func listDocs(ctx context.Context, svc documents.Service, in docListInput) (any, string, error) {
	var f documents.Filter
	switch {
	case in.ProjectOnly && in.BriefID != "":
		return nil, "", fmt.Errorf("%w: project_only cannot be combined with brief_id", documents.ErrInvalid)
	case in.ProjectOnly:
		none := ""
		f.BriefID = &none
	case in.BriefID != "":
		id := normalizeID(in.BriefID)
		f.BriefID = &id
	}
	for _, k := range in.Kinds {
		f.Kinds = append(f.Kinds, documents.Kind(k))
	}
	for _, st := range in.Statuses {
		f.Statuses = append(f.Statuses, documents.Status(st))
	}
	list, err := svc.List(ctx, f)
	if err != nil {
		return nil, "", err
	}
	out := docListResult{Documents: []docSummary{}}
	var b strings.Builder
	for _, d := range list {
		out.Documents = append(out.Documents, docSummary{
			ID: d.ID, Kind: string(d.Kind), BriefID: d.BriefID, Title: d.Title, Status: string(d.Status),
			SupersededBy: d.SupersededBy, Version: d.Version, UpdatedAt: formatTime(d.UpdatedAt), Fingerprint: d.Fingerprint,
		})
		fmt.Fprintf(&b, "%s %s %s %s (fingerprint %s)\n", d.ID, d.Kind, d.Status, d.Title, d.Fingerprint)
	}
	if len(list) == 0 {
		return out, "No documents match.", nil
	}
	return out, strings.TrimRight(b.String(), "\n"), nil
}
