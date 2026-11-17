package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/view"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var unitIDPattern = regexp.MustCompile(`^[BST]-[0-9]+$`)

const levelsText = `Work has three levels. A brief (ID like B-1) is a request or goal as the user stated it; its text is kept verbatim and cannot be edited. A slice (S-1) is one independently deliverable part of a brief. A task (T-1) is one concrete step, small enough for a single working session. Only tasks have a state of their own (PROPOSED, TODO, IN_PROGRESS, BLOCKED, DONE or CANCELLED) and only tasks have acceptance criteria; a brief's and a slice's status is derived from the tasks beneath it. A task with no slice is an orphan task.`

const fingerprintText = `Every unit carries a fingerprint that changes whenever the unit changes. Updates need the fingerprint from your latest read of the unit (unit_get, unit_list or the result of an earlier write); if another writer changed the unit since, the call fails with error_code "stale" and you must call unit_get and retry.`

var (
	levelEnum = []any{"brief", "slice", "task"}
	stateEnum = []any{"PROPOSED", "TODO", "IN_PROGRESS", "BLOCKED", "DONE", "CANCELLED"}
	kindEnum  = []any{"document", "external"}

	noteKindEnum = []any{"NOTE", "KICKBACK"}
)

// unitResult is a unit's detail plus the fingerprint to pass on the next write.
type unitResult struct {
	view.UnitView
	Fingerprint string `json:"fingerprint"`
}

type unitSummary struct {
	ID          string `json:"id"`
	Level       string `json:"level"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	ParentID    string `json:"parent_id"`
	Position    int    `json:"position"`
	Fingerprint string `json:"fingerprint"`
}

type listResult struct {
	Units []unitSummary `json:"units"`
}

type linksResult struct {
	ID    string      `json:"id"`
	Links []view.Link `json:"links"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"unit ID such as B-1, S-2 or T-3"`
}

type listInput struct {
	Level    string   `json:"level,omitempty" jsonschema:"only units of this level"`
	ParentID string   `json:"parent_id,omitempty" jsonschema:"only direct children of this unit"`
	NoParent bool     `json:"no_parent,omitempty" jsonschema:"only units with no parent (briefs, slices without a brief and orphan tasks); cannot be combined with parent_id"`
	States   []string `json:"states,omitempty" jsonschema:"only tasks in one of these states; briefs and slices have no state and never match"`
}

type createInput struct {
	Level              string `json:"level" jsonschema:"brief, slice or task"`
	ParentID           string `json:"parent_id,omitempty" jsonschema:"parent unit ID: a brief for a slice, a slice for a task; omit for a brief or an orphan task"`
	Title              string `json:"title" jsonschema:"short title, not blank"`
	Body               string `json:"body,omitempty" jsonschema:"description; for a brief, the user's words verbatim"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty" jsonschema:"how to verify the work; tasks only"`
	Position           int    `json:"position,omitempty" jsonschema:"1-based place among siblings; omit to append"`
}

type updateInput struct {
	ID                 string  `json:"id" jsonschema:"unit ID such as S-2 or T-3"`
	Fingerprint        string  `json:"fingerprint" jsonschema:"fingerprint from your latest read of this unit"`
	Title              *string `json:"title,omitempty" jsonschema:"new title; omit to keep"`
	Body               *string `json:"body,omitempty" jsonschema:"new body; omit to keep; not allowed on a brief"`
	AcceptanceCriteria *string `json:"acceptance_criteria,omitempty" jsonschema:"new acceptance criteria; tasks only; omit to keep"`
	Position           *int    `json:"position,omitempty" jsonschema:"new 1-based place among siblings; omit to keep"`
}

type setStateInput struct {
	ID          string `json:"id" jsonschema:"task ID such as T-3"`
	Fingerprint string `json:"fingerprint" jsonschema:"fingerprint from your latest read of this task"`
	State       string `json:"state" jsonschema:"the state to move to"`
	Reason      string `json:"reason,omitempty" jsonschema:"why; recorded as a note on the task"`
}

type noteInput struct {
	ID   string `json:"id" jsonschema:"unit ID such as B-1, S-2 or T-3"`
	Body string `json:"body" jsonschema:"the note text"`
	Kind string `json:"kind,omitempty" jsonschema:"NOTE (the default) or KICKBACK"`
}

type dependencyInput struct {
	ID        string `json:"id" jsonschema:"the slice that waits, a slice ID such as S-2"`
	DependsOn string `json:"depends_on" jsonschema:"the slice it waits for, a slice ID such as S-1"`
}

type linkInput struct {
	ID     string `json:"id" jsonschema:"unit ID such as B-1, S-2 or T-3"`
	Kind   string `json:"kind" jsonschema:"document for a project document, external for an item in an outside system"`
	Target string `json:"target" jsonschema:"what to link to: a document ID or an outside reference, with no whitespace"`
}

// schemaFor builds the input schema for In and lets edit restrict properties.
func schemaFor[In any](edit func(props map[string]*jsonschema.Schema)) *jsonschema.Schema {
	sc, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: input schema: %v", err))
	}
	if edit != nil {
		edit(sc.Properties)
	}
	return sc
}

func outputFor[Out any]() *jsonschema.Schema {
	sc, err := jsonschema.For[Out](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: output schema: %v", err))
	}
	return sc
}

func enum(values []any) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: values}
}

func (s *server) addUnitTools(srv *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	writes := func(idempotent bool) *mcp.ToolAnnotations {
		no := false
		return &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: idempotent}
	}

	addTool(s, srv, &mcp.Tool{
		Name: "unit_get",
		Description: `Read one unit: its title, body, acceptance criteria, derived status, parent, children (id, level, title, status), links, the documents linked to it (documents: id, title, kind, status, and superseded_by for a superseded one; MISSING when the link names no stored document) and the documents inherited from the slice and brief above it (inherited_documents, each with from, the ancestor it comes from), notes (oldest first) and its fingerprint. A slice also shows its dependencies: depends_on (id, title, status and whether each is satisfied) and waiting_on (the IDs not yet satisfied), both left out when it has none. A slice also lists its pull_requests (number, url, state and resolution, oldest first), left out when it has none; its status is NEEDS_ATTENTION when the latest one was closed without merging and has no decision yet. Call this before updating a unit, and again after a "stale" error.

` + levelsText + `

` + fingerprintText,
		Annotations:  readOnly,
		InputSchema:  schemaFor[idInput](nil),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in idInput) (any, string, error) {
		id, err := parseID(in.ID)
		if err != nil {
			return nil, "", err
		}
		return s.unitOut(ctx, svc, id)
	})

	addTool(s, srv, &mcp.Tool{
		Name: "unit_list",
		Description: `List unit summaries (id, level, title, status or state, parent_id, position, fingerprint) in the project's own order. All filters are optional and combine: level, parent_id (direct children of that unit), no_parent (units with no parent; cannot be combined with parent_id) and states (tasks in any of those states).

` + levelsText,
		Annotations: readOnly,
		InputSchema: schemaFor[listInput](func(p map[string]*jsonschema.Schema) {
			p["level"] = enum(levelEnum)
			p["states"].Items = enum(stateEnum)
		}),
		OutputSchema: outputFor[listResult](),
	}, func(ctx context.Context, svc units.Service, in listInput) (any, string, error) {
		return listUnits(ctx, svc, in)
	})

	addTool(s, srv, &mcp.Tool{
		Name: "unit_create",
		Description: `Create a brief, a slice or a task and return it with its fingerprint. A new task starts in PROPOSED.

` + levelsText + `

Parents: a slice's parent_id is a brief; a task's parent_id is a slice, or omitted for an orphan task. A brief has no parent. Acceptance criteria are for tasks only. A brief's body is stored verbatim and cannot be edited afterwards.`,
		InputSchema: schemaFor[createInput](func(p map[string]*jsonschema.Schema) {
			p["level"] = enum(levelEnum)
		}),
		Annotations:  writes(false),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in createInput) (any, string, error) {
		parent := ""
		if in.ParentID != "" {
			var err error
			if parent, err = parseID(in.ParentID); err != nil {
				return nil, "", err
			}
		}
		u, err := svc.Create(ctx, units.NewUnit{
			Level: units.Level(in.Level), ParentID: parent, Title: in.Title, Body: in.Body,
			AcceptanceCriteria: in.AcceptanceCriteria, Position: in.Position,
		})
		if err != nil {
			return nil, "", err
		}
		return s.unitOut(ctx, svc, u.ID)
	})

	addTool(s, srv, &mcp.Tool{
		Name: "unit_update",
		Description: `Change a unit's title, body, acceptance criteria or position; only the fields you give change. Returns the unit with its new fingerprint. A brief's body cannot be edited, acceptance criteria exist only on tasks, and a finished task (DONE or CANCELLED) cannot be edited. A task in TODO or IN_PROGRESS cannot be edited into an unready shape (no description, or, for a task in a slice, no acceptance criteria); move it back to PROPOSED first. To change a task's state use task_set_state.

` + fingerprintText,
		Annotations:  writes(true),
		InputSchema:  schemaFor[updateInput](nil),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in updateInput) (any, string, error) {
		id, err := parseID(in.ID)
		if err != nil {
			return nil, "", err
		}
		u, err := svc.Update(ctx, id, in.Fingerprint, units.Change{
			Title: in.Title, Body: in.Body, AcceptanceCriteria: in.AcceptanceCriteria, Position: in.Position,
		})
		if err != nil {
			return nil, "", err
		}
		return s.unitOut(ctx, svc, u.ID)
	})

	addTool(s, srv, &mcp.Tool{
		Name: "task_set_state",
		Description: `Move a task to another state: PROPOSED, TODO, IN_PROGRESS, BLOCKED, DONE or CANCELLED. Only tasks have a state; a brief's or slice's status is derived. If the move is not allowed the call fails with error_code "invalid_transition" and lists the states the task may move to. Moving a task to TODO needs a description and, for a task in a slice, acceptance criteria; otherwise the call fails with error_code "not_ready" and lists what is missing. Moving a task to BLOCKED, to PROPOSED or to CANCELLED needs a reason; without one the call fails with error_code "reason_required". Moving a task to PROPOSED is a kickback: it says the task, or the plan around it, needs to be planned again, and the reason is recorded as a KICKBACK note. For any other move the reason is optional. Moving a slice's task to IN_PROGRESS is refused with error_code "waiting" while the slice waits for other slices that are not DONE; the error lists them in waiting_on, and finishing them, or removing the dependency with dependency_remove, lets the task start. A reason is recorded as a note. Returns the task with its new fingerprint.

` + fingerprintText,
		Annotations: writes(false),
		InputSchema: schemaFor[setStateInput](func(p map[string]*jsonschema.Schema) {
			p["state"] = enum(stateEnum)
		}),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in setStateInput) (any, string, error) {
		id, err := parseID(in.ID)
		if err != nil {
			return nil, "", err
		}
		u, err := svc.SetState(ctx, id, in.Fingerprint, units.State(in.State), in.Reason)
		if err != nil {
			return nil, "", err
		}
		return s.unitOut(ctx, svc, u.ID)
	})

	addTool(s, srv, &mcp.Tool{
		Name:        "note_add",
		Description: `Add a note to a brief, a slice or a task, such as progress, a decision or a question. Notes are append-only, are shown oldest first by unit_get and do not change the unit's fingerprint. The kind is NOTE by default; a KICKBACK note on a PROPOSED or TODO task records that the task or the plan around it needs to be planned again. Returns the note.`,
		Annotations: writes(false),
		InputSchema: schemaFor[noteInput](func(p map[string]*jsonschema.Schema) {
			p["kind"] = enum(noteKindEnum)
		}),
		OutputSchema: outputFor[view.Note](),
	}, func(ctx context.Context, svc units.Service, in noteInput) (any, string, error) {
		id, err := parseID(in.ID)
		if err != nil {
			return nil, "", err
		}
		kind := units.NoteKind(in.Kind)
		if in.Kind == "" {
			kind = units.NoteGeneral
		}
		n, err := svc.AddNote(ctx, id, kind, in.Body)
		if err != nil {
			return nil, "", err
		}
		return noteView(n), fmt.Sprintf("Added a note to %s.", id), nil
	})

	const dependencyText = `Order between slices is recorded as a dependency: a slice waits for another slice. A dependency is satisfied when the slice it waits on is DONE. Any two slices may be tied, even in different briefs. A dependency that would make a cycle is rejected with error_code "invalid". Dependencies need no fingerprint. While a slice waits, its tasks cannot be moved to IN_PROGRESS. Returns the slice, with its dependencies in depends_on and waiting_on, as unit_get does.`
	addTool(s, srv, &mcp.Tool{
		Name:         "dependency_add",
		Description:  `Make a slice wait for another slice (id waits for depends_on). Adding one that exists changes nothing. ` + dependencyText,
		Annotations:  writes(true),
		InputSchema:  schemaFor[dependencyInput](nil),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in dependencyInput) (any, string, error) {
		return s.changeDependency(ctx, svc, in, svc.AddDependency)
	})
	addTool(s, srv, &mcp.Tool{
		Name:         "dependency_remove",
		Description:  `Remove the dependency of a slice (id) on another slice (depends_on); removing one that does not exist changes nothing. ` + dependencyText,
		Annotations:  writes(true),
		InputSchema:  schemaFor[dependencyInput](nil),
		OutputSchema: outputFor[unitResult](),
	}, func(ctx context.Context, svc units.Service, in dependencyInput) (any, string, error) {
		return s.changeDependency(ctx, svc, in, svc.RemoveDependency)
	})

	linkSchema := func(p map[string]*jsonschema.Schema) { p["kind"] = enum(kindEnum) }
	addTool(s, srv, &mcp.Tool{
		Name:         "link_add",
		Description:  `Link a unit to a project document (kind "document", target a document ID) or to an item in an outside system (kind "external", target a reference such as a URL or issue key, with no whitespace). A document link must name an ACTIVE document: a missing one fails with error_code "not_found", and an archived or superseded one with error_code "invalid" (for a superseded one the message names its replacement, so link that instead; for an archived one, ask the user whether to restore it with doc_restore or to link another). Adding a link that exists changes nothing. Returns the unit's links.`,
		Annotations:  writes(true),
		InputSchema:  schemaFor[linkInput](linkSchema),
		OutputSchema: outputFor[linksResult](),
	}, func(ctx context.Context, svc units.Service, in linkInput) (any, string, error) {
		return s.changeLink(ctx, svc, in, true)
	})
	addTool(s, srv, &mcp.Tool{
		Name:         "link_remove",
		Description:  `Remove a link from a unit; removing a link that does not exist changes nothing. Kind and target are as given to link_add. A link to a document of any status, even one that no longer exists, can be removed. Returns the unit's links.`,
		Annotations:  writes(true),
		InputSchema:  schemaFor[linkInput](linkSchema),
		OutputSchema: outputFor[linksResult](),
	}, func(ctx context.Context, svc units.Service, in linkInput) (any, string, error) {
		return s.changeLink(ctx, svc, in, false)
	})
}

func parseID(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if !unitIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: %q is not a unit ID; use B-<n>, S-<n> or T-<n>", units.ErrInvalid, raw)
	}
	return id, nil
}

func noteView(n units.Note) view.Note {
	return view.Note{Kind: string(n.Kind), Author: n.Author, Body: n.Body, CreatedAt: n.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")}
}

// docLinks builds the document link service over the documents service the
// document tools use.
func (s *server) docLinks(ctx context.Context, svc units.Service) (*doclinks.Service, error) {
	if s.opts.Documents == nil {
		return nil, errors.New("documents are not available in this server")
	}
	docs, err := s.documents(ctx)
	if err != nil {
		return nil, err
	}
	return doclinks.New(svc, docs), nil
}

func (s *server) unitOut(ctx context.Context, svc units.Service, id string) (any, string, error) {
	u, err := svc.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	d, err := view.UnitDetail(ctx, svc, id)
	if err != nil {
		return nil, "", err
	}
	if s.opts.Documents != nil {
		if links, err := s.docLinks(ctx, svc); err != nil {
			s.queueNotice(Notice{Code: "documents_unreadable", Message: "The documents linked to this unit could not be read: " + err.Error()})
		} else if d, err = view.AttachDocuments(ctx, d, links); err != nil {
			return nil, "", err
		}
	}
	return unitResult{UnitView: d, Fingerprint: u.Fingerprint}, unitText(d, u.Fingerprint), nil
}

func unitText(d view.UnitView, fingerprint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\nfingerprint: %s\n", d.ID, d.Status, d.Title, fingerprint)
	if d.Parent != nil {
		fmt.Fprintf(&b, "parent: %s %s\n", d.Parent.ID, d.Parent.Title)
	}
	if d.Body != "" {
		fmt.Fprintf(&b, "\n%s\n", d.Body)
	}
	if d.AcceptanceCriteria != "" {
		fmt.Fprintf(&b, "\nAcceptance criteria:\n%s\n", d.AcceptanceCriteria)
	}
	for _, c := range d.Children {
		fmt.Fprintf(&b, "child: %s %s %s\n", c.ID, c.Status, c.Title)
	}
	for _, dep := range d.DependsOn {
		waiting := ""
		if !dep.Satisfied {
			waiting = " (waiting)"
		}
		fmt.Fprintf(&b, "depends on: %s %s %s%s\n", dep.ID, dep.Status, dep.Title, waiting)
	}
	for _, l := range d.Links {
		fmt.Fprintf(&b, "link: %s %s\n", l.Kind, l.Target)
	}
	for _, l := range d.Documents {
		fmt.Fprintf(&b, "document: %s\n", documentLine(l))
	}
	for _, l := range d.InheritedDocuments {
		fmt.Fprintf(&b, "document (from %s): %s\n", l.From, documentLine(l))
	}
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "note (%s %s): %s\n", n.CreatedAt, n.Author, n.Body)
	}
	return strings.TrimRight(b.String(), "\n")
}

func documentLine(l doclinks.Linked) string {
	switch l.Status {
	case doclinks.StatusMissing:
		return l.ID + " " + l.Status
	case string(documents.StatusSuperseded):
		return fmt.Sprintf("%s %s SUPERSEDED by %s %s", l.ID, l.Kind, l.SupersededBy, l.Title)
	}
	return fmt.Sprintf("%s %s %s %s", l.ID, l.Kind, l.Status, l.Title)
}

func listUnits(ctx context.Context, svc units.Service, in listInput) (any, string, error) {
	f := units.Filter{Level: units.Level(in.Level)}
	switch {
	case in.NoParent && in.ParentID != "":
		return nil, "", fmt.Errorf("%w: no_parent cannot be combined with parent_id", units.ErrInvalid)
	case in.NoParent:
		none := ""
		f.ParentID = &none
	case in.ParentID != "":
		id, err := parseID(in.ParentID)
		if err != nil {
			return nil, "", err
		}
		f.ParentID = &id
	}
	for _, st := range in.States {
		f.States = append(f.States, units.State(st))
	}
	list, err := svc.List(ctx, f)
	if err != nil {
		return nil, "", err
	}
	var derived []string
	for _, u := range list {
		if u.Level != units.LevelTask {
			derived = append(derived, u.ID)
		}
	}
	statuses, err := svc.Statuses(ctx, derived)
	if err != nil {
		return nil, "", err
	}
	out := listResult{Units: []unitSummary{}}
	var b strings.Builder
	for _, u := range list {
		st := string(u.State)
		if u.Level != units.LevelTask {
			st = string(statuses[u.ID])
		}
		out.Units = append(out.Units, unitSummary{
			ID: u.ID, Level: string(u.Level), Title: u.Title, Status: st,
			ParentID: u.ParentID, Position: u.Position, Fingerprint: u.Fingerprint,
		})
		fmt.Fprintf(&b, "%s %s %s (fingerprint %s)\n", u.ID, st, u.Title, u.Fingerprint)
	}
	if len(list) == 0 {
		return out, "No units match.", nil
	}
	return out, strings.TrimRight(b.String(), "\n"), nil
}

func (s *server) changeDependency(ctx context.Context, svc units.Service, in dependencyInput, op func(context.Context, string, string) error) (any, string, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return nil, "", err
	}
	on, err := parseID(in.DependsOn)
	if err != nil {
		return nil, "", err
	}
	if err := op(ctx, id, on); err != nil {
		return nil, "", err
	}
	return s.unitOut(ctx, svc, id)
}

func (s *server) changeLink(ctx context.Context, svc units.Service, in linkInput, add bool) (any, string, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return nil, "", err
	}
	if units.LinkKind(in.Kind) == units.LinkDocument {
		links, err := s.docLinks(ctx, svc)
		if err != nil {
			return nil, "", err
		}
		if add {
			err = links.Link(ctx, in.Target, id)
		} else {
			err = links.Unlink(ctx, in.Target, id)
		}
		if err != nil {
			return nil, "", err
		}
	} else {
		op := svc.RemoveLink
		if add {
			op = svc.AddLink
		}
		if err := op(ctx, id, units.LinkKind(in.Kind), in.Target); err != nil {
			return nil, "", err
		}
	}
	links, err := svc.Links(ctx, id)
	if err != nil {
		return nil, "", err
	}
	out := linksResult{ID: id, Links: []view.Link{}}
	var b strings.Builder
	fmt.Fprintf(&b, "Links of %s:", id)
	for _, l := range links {
		out.Links = append(out.Links, view.Link{Kind: string(l.Kind), Target: l.Target})
		fmt.Fprintf(&b, "\n%s %s", l.Kind, l.Target)
	}
	if len(links) == 0 {
		b.WriteString(" none")
	}
	return out, b.String(), nil
}
