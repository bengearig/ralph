package mcpserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const actor = "Ann <ann@example.com>"

func newSvc(t *testing.T) units.Service {
	t.Helper()
	return units.NewService(testutil.NewDB(t, db.Migrations))
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func ok(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: error result: %s", name, text(res))
	}
	m, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v", name, res.StructuredContent)
	}
	return m
}

func fails(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, code string) *mcp.CallToolResult {
	t.Helper()
	res := call(t, cs, name, args)
	if got := errorCode(t, res); got != code {
		t.Fatalf("%s: error_code = %q (%s), want %q", name, got, text(res), code)
	}
	return res
}

func newClient(t *testing.T) (*mcp.ClientSession, units.Service) {
	t.Helper()
	svc := newSvc(t)
	return connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(svc)}), svc
}

func create(t *testing.T, cs *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	return ok(t, cs, "unit_create", args)
}

func tools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		m[tool.Name] = tool
	}
	return m
}

func schemaOf(t *testing.T, tool *mcp.Tool) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var sc jsonschema.Schema
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	return &sc
}

func enumStrings(sc *jsonschema.Schema) []string {
	var out []string
	for _, v := range sc.Enum {
		out = append(out, v.(string))
	}
	return out
}

func TestToolsList(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	readOnly := map[string]bool{"status": true, "unit_get": true, "unit_list": true}
	for _, name := range []string{"status", "unit_get", "unit_list", "unit_create", "unit_update", "task_set_state", "note_add", "link_add", "link_remove"} {
		tool, found := got[name]
		if !found {
			t.Errorf("tool %s not listed", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", name)
		}
		if ro := tool.Annotations != nil && tool.Annotations.ReadOnlyHint; ro != readOnly[name] {
			t.Errorf("%s read-only = %v, want %v", name, ro, readOnly[name])
		}
	}
}

func TestToolSchemasEnumerate(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	levels := []string{"brief", "slice", "task"}
	states := []string{"PROPOSED", "TODO", "IN_PROGRESS", "BLOCKED", "DONE", "CANCELLED"}
	kinds := []string{"document", "external"}
	check := func(tool, prop string, sc *jsonschema.Schema, want []string) {
		t.Helper()
		if sc == nil || !slices.Equal(enumStrings(sc), want) {
			t.Errorf("%s.%s enum = %v, want %v", tool, prop, sc, want)
		}
	}
	check("unit_create", "level", schemaOf(t, got["unit_create"]).Properties["level"], levels)
	check("unit_list", "level", schemaOf(t, got["unit_list"]).Properties["level"], levels)
	check("unit_list", "states", schemaOf(t, got["unit_list"]).Properties["states"].Items, states)
	check("task_set_state", "state", schemaOf(t, got["task_set_state"]).Properties["state"], states)
	check("link_add", "kind", schemaOf(t, got["link_add"]).Properties["kind"], kinds)
	check("link_remove", "kind", schemaOf(t, got["link_remove"]).Properties["kind"], kinds)
}

func TestToolSchemasRequireFingerprint(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	for _, name := range []string{"unit_update", "task_set_state"} {
		if req := schemaOf(t, got[name]).Required; !slices.Contains(req, "fingerprint") || !slices.Contains(req, "id") {
			t.Errorf("%s required = %v", name, req)
		}
	}
	if req := schemaOf(t, got["task_set_state"]).Required; !slices.Contains(req, "state") {
		t.Errorf("task_set_state required = %v", req)
	}
	if req := schemaOf(t, got["unit_create"]).Required; !slices.Contains(req, "level") || !slices.Contains(req, "title") {
		t.Errorf("unit_create required = %v", req)
	}
}

func TestToolDescriptionsMentionLevels(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	for _, word := range []string{"brief", "slice", "task", "orphan", "verbatim", "acceptance criteria"} {
		if !strings.Contains(strings.ToLower(got["unit_create"].Description), word) {
			t.Errorf("unit_create description lacks %q", word)
		}
	}
	for _, name := range []string{"unit_update", "task_set_state"} {
		if !strings.Contains(got[name].Description, "fingerprint") {
			t.Errorf("%s description lacks fingerprint", name)
		}
	}
}

func TestUnitCreateBriefSliceTask(t *testing.T) {
	cs, svc := newClient(t)
	b := create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "do it"})
	s := create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "parent_id": "S-1", "acceptance_criteria": "works"})
	for i, want := range []string{"B-1", "S-1", "T-1"} {
		if got := []map[string]any{b, s, tk}[i]["id"]; got != want {
			t.Errorf("id = %v, want %s", got, want)
		}
	}
	if tk["status"] != "PROPOSED" || tk["fingerprint"] == "" {
		t.Errorf("task = %v", tk)
	}
	u, err := svc.Get(context.Background(), "T-1")
	if err != nil || u.ParentID != "S-1" || u.AcceptanceCriteria != "works" {
		t.Errorf("stored = %+v, %v", u, err)
	}
	if sl, _ := svc.Get(context.Background(), "S-1"); sl.ParentID != "B-1" {
		t.Errorf("slice parent = %q", sl.ParentID)
	}
}

func TestUnitCreateOrphanTask(t *testing.T) {
	cs, svc := newClient(t)
	got := create(t, cs, map[string]any{"level": "task", "title": "loose", "acceptance_criteria": "c"})
	u, err := svc.Get(context.Background(), got["id"].(string))
	if err != nil || u.ParentID != "" {
		t.Errorf("stored = %+v, %v", u, err)
	}
	if got["parent"] != nil {
		t.Errorf("parent = %v", got["parent"])
	}
}

func TestUnitCreateInvalidParent(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	fails(t, cs, "unit_create", map[string]any{"level": "brief", "title": "x", "body": "g", "parent_id": "B-1"}, "invalid_parent")
	fails(t, cs, "unit_create", map[string]any{"level": "task", "title": "x", "acceptance_criteria": "c", "parent_id": "B-1"}, "invalid_parent")
}

func TestUnitCreateInvalidInput(t *testing.T) {
	cs, _ := newClient(t)
	fails(t, cs, "unit_create", map[string]any{"level": "brief", "title": "  "}, "invalid")
	fails(t, cs, "unit_create", map[string]any{"level": "slice", "title": "x", "parent_id": "nonsense"}, "invalid")
}

func TestUnitGetDetail(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	create(t, cs, map[string]any{"level": "task", "title": "T", "parent_id": "S-1", "acceptance_criteria": "c"})
	ok(t, cs, "link_add", map[string]any{"id": "S-1", "kind": "external", "target": "JIRA-1"})
	ok(t, cs, "note_add", map[string]any{"id": "S-1", "body": "first"})
	ok(t, cs, "note_add", map[string]any{"id": "S-1", "body": "second"})

	got := ok(t, cs, "unit_get", map[string]any{"id": "S-1"})
	if got["status"] != "ARTICULATING" || got["fingerprint"] == "" {
		t.Errorf("got = %v", got)
	}
	kids := got["children"].([]any)
	if len(kids) != 1 || kids[0].(map[string]any)["id"] != "T-1" || kids[0].(map[string]any)["status"] != "PROPOSED" {
		t.Errorf("children = %v", kids)
	}
	links := got["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["target"] != "JIRA-1" {
		t.Errorf("links = %v", links)
	}
	notes := got["notes"].([]any)
	if len(notes) != 2 || notes[0].(map[string]any)["body"] != "first" || notes[1].(map[string]any)["body"] != "second" {
		t.Errorf("notes = %v", notes)
	}
	if b := ok(t, cs, "unit_get", map[string]any{"id": "b-1"}); b["id"] != "B-1" {
		t.Errorf("lowercase id: %v", b)
	}
}

func TestUnitGetNotFound(t *testing.T) {
	cs, _ := newClient(t)
	fails(t, cs, "unit_get", map[string]any{"id": "T-99"}, "not_found")
}

func TestUnitGetBadID(t *testing.T) {
	cs, _ := newClient(t)
	for _, id := range []string{"", "X-1", "T-", "T-1; DROP", "1"} {
		fails(t, cs, "unit_get", map[string]any{"id": id}, "invalid")
	}
}

func ids(t *testing.T, res map[string]any) []string {
	t.Helper()
	var out []string
	for _, u := range res["units"].([]any) {
		out = append(out, u.(map[string]any)["id"].(string))
	}
	slices.Sort(out)
	return out
}

func TestUnitListFilters(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	create(t, cs, map[string]any{"level": "task", "title": "in", "parent_id": "S-1", "acceptance_criteria": "c"})
	o := create(t, cs, map[string]any{"level": "task", "title": "orphan", "body": "b", "acceptance_criteria": "c"})
	ok(t, cs, "task_set_state", map[string]any{"id": "T-2", "fingerprint": o["fingerprint"], "state": "TODO"})

	cases := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"all", nil, []string{"B-1", "S-1", "T-1", "T-2"}},
		{"level", map[string]any{"level": "task"}, []string{"T-1", "T-2"}},
		{"parent", map[string]any{"parent_id": "S-1"}, []string{"T-1"}},
		{"no parent", map[string]any{"no_parent": true}, []string{"B-1", "T-2"}},
		{"orphan tasks", map[string]any{"no_parent": true, "level": "task"}, []string{"T-2"}},
		{"states", map[string]any{"states": []string{"TODO"}}, []string{"T-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(t, ok(t, cs, "unit_list", c.args)); !slices.Equal(got, c.want) {
				t.Errorf("ids = %v, want %v", got, c.want)
			}
		})
	}
	first := ok(t, cs, "unit_list", map[string]any{"level": "slice"})["units"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "level", "title", "status", "parent_id", "position", "fingerprint"} {
		if _, has := first[k]; !has {
			t.Errorf("summary lacks %s: %v", k, first)
		}
	}
	if first["status"] != "ARTICULATING" || first["parent_id"] != "B-1" {
		t.Errorf("summary = %v", first)
	}
}

func TestUnitListNoParentWithParentInvalid(t *testing.T) {
	cs, _ := newClient(t)
	fails(t, cs, "unit_list", map[string]any{"no_parent": true, "parent_id": "B-1"}, "invalid")
}

func TestUnitUpdateChangesFields(t *testing.T) {
	cs, svc := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "keep", "parent_id": "S-1", "acceptance_criteria": "old"})
	got := ok(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "title": "New", "acceptance_criteria": "new"})
	if got["title"] != "New" || got["acceptance_criteria"] != "new" || got["body"] != "keep" {
		t.Errorf("got = %v", got)
	}
	if got["fingerprint"] == tk["fingerprint"] {
		t.Error("fingerprint did not change")
	}
	u, _ := svc.Get(context.Background(), "T-1")
	if u.Fingerprint != got["fingerprint"] {
		t.Errorf("fingerprint %v does not match stored %s", got["fingerprint"], u.Fingerprint)
	}
}

type countingService struct {
	units.Service
	updates, states atomic.Int32
}

func (c *countingService) Update(ctx context.Context, id, fp string, ch units.Change) (units.Unit, error) {
	c.updates.Add(1)
	return c.Service.Update(ctx, id, fp, ch)
}

func (c *countingService) SetState(ctx context.Context, id, fp string, to units.State, reason string) (units.Unit, error) {
	c.states.Add(1)
	return c.Service.SetState(ctx, id, fp, to, reason)
}

func TestUnitUpdateMissingFingerprintRejected(t *testing.T) {
	svc := &countingService{Service: newSvc(t)}
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(svc)})
	create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	for name, args := range map[string]map[string]any{
		"unit_update":    {"id": "T-1", "title": "x"},
		"task_set_state": {"id": "T-1", "state": "TODO"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && (res == nil || !res.IsError) {
			t.Errorf("%s accepted a call without a fingerprint", name)
		}
	}
	if svc.updates.Load() != 0 || svc.states.Load() != 0 {
		t.Errorf("service reached: updates %d, states %d", svc.updates.Load(), svc.states.Load())
	}
}

func TestUnitUpdateStale(t *testing.T) {
	cs, svc := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	title := "second writer"
	if _, err := svc.Update(context.Background(), "T-1", tk["fingerprint"].(string), units.Change{Title: &title}); err != nil {
		t.Fatal(err)
	}
	res := fails(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "title": "mine"}, "stale")
	if !strings.Contains(text(res), "unit_get") {
		t.Errorf("text = %q", text(res))
	}
	if u, _ := svc.Get(context.Background(), "T-1"); u.Title != title {
		t.Errorf("title = %q", u.Title)
	}
}

func TestUnitUpdateBriefBodyRejected(t *testing.T) {
	cs, _ := newClient(t)
	b := create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "verbatim"})
	fails(t, cs, "unit_update", map[string]any{"id": "B-1", "fingerprint": b["fingerprint"], "body": "edited"}, "invalid")
}

func TestTaskSetStateTransition(t *testing.T) {
	cs, _ := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	got := ok(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "TODO", "reason": "ready"})
	if got["status"] != "TODO" || got["fingerprint"] == tk["fingerprint"] {
		t.Errorf("got = %v", got)
	}
	if notes := got["notes"].([]any); len(notes) != 1 || !strings.Contains(notes[0].(map[string]any)["body"].(string), "ready") {
		t.Errorf("notes = %v", notes)
	}
}

func TestTaskSetStateInvalidTransition(t *testing.T) {
	cs, _ := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	res := fails(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "DONE"}, "invalid_transition")
	want := units.AllowedNext(units.StateProposed)
	sc := res.StructuredContent.(map[string]any)
	got, _ := sc["allowed"].([]any)
	if len(got) != len(want) || len(want) == 0 {
		t.Fatalf("allowed = %v, want %v", got, want)
	}
	for i, st := range want {
		if got[i] != string(st) || !strings.Contains(text(res), string(st)) {
			t.Errorf("allowed[%d] = %v, text %q", i, got[i], text(res))
		}
	}
}

func TestTaskSetStateStale(t *testing.T) {
	cs, _ := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	ok(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "TODO"})
	res := fails(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "IN_PROGRESS"}, "stale")
	if !strings.Contains(text(res), "unit_get") {
		t.Errorf("text = %q", text(res))
	}
}

func TestTaskSetStateOnSliceInvalid(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	s := create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	fails(t, cs, "task_set_state", map[string]any{"id": "S-1", "fingerprint": s["fingerprint"], "state": "TODO"}, "invalid")
}

func TestNoteAdd(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	got := ok(t, cs, "note_add", map[string]any{"id": "B-1", "body": "remember"})
	if got["body"] != "remember" || got["author"] != actor || got["kind"] != "NOTE" {
		t.Errorf("note = %v", got)
	}
	fails(t, cs, "note_add", map[string]any{"id": "B-9", "body": "x"}, "not_found")
}

func TestLinkAddRemove(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	args := map[string]any{"id": "B-1", "kind": "document", "target": "D-1"}
	for range 2 {
		if got := ok(t, cs, "link_add", args); len(got["links"].([]any)) != 1 {
			t.Errorf("links = %v", got["links"])
		}
	}
	for range 2 {
		if got := ok(t, cs, "link_remove", args); len(got["links"].([]any)) != 0 {
			t.Errorf("links = %v", got["links"])
		}
	}
	fails(t, cs, "link_add", map[string]any{"id": "B-1", "kind": "document", "target": "has space"}, "invalid")
}

func TestWritesRecordActor(t *testing.T) {
	cs, svc := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	ok(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "title": "U"})
	ok(t, cs, "note_add", map[string]any{"id": "T-1", "body": "n"})
	u, err := svc.Get(context.Background(), "T-1")
	if err != nil || u.CreatedBy != actor || u.UpdatedBy != actor {
		t.Errorf("unit = %+v, %v", u, err)
	}
	notes, err := svc.Notes(context.Background(), "T-1")
	if err != nil || len(notes) != 1 || notes[0].Author != actor {
		t.Errorf("notes = %+v, %v", notes, err)
	}
}

func TestResultsAlwaysIncludeFingerprint(t *testing.T) {
	cs, _ := newClient(t)
	check := func(name string, got map[string]any) {
		t.Helper()
		if fp, _ := got["fingerprint"].(string); fp == "" {
			t.Errorf("%s result lacks fingerprint: %v", name, got)
		}
	}
	b := create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	check("unit_create", b)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b", "acceptance_criteria": "c"})
	check("unit_create", tk)
	check("unit_get", ok(t, cs, "unit_get", map[string]any{"id": "T-1"}))
	check("unit_update", ok(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "title": "U"}))
	cur := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})
	check("task_set_state", ok(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": cur["fingerprint"], "state": "TODO"}))
	for _, u := range ok(t, cs, "unit_list", nil)["units"].([]any) {
		check("unit_list", u.(map[string]any))
	}
}

func TestTaskSetStateNotReadyCode(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "parent_id": "S-1"})
	res := fails(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "TODO"}, "not_ready")
	missing, _ := res.StructuredContent.(map[string]any)["missing"].([]any)
	if len(missing) != 2 || missing[0] != "description" || missing[1] != "acceptance criteria" {
		t.Errorf("missing = %v", missing)
	}
	if !strings.Contains(text(res), "unit_update") || !strings.Contains(text(res), "not ready: T-1 needs") {
		t.Errorf("text = %q", text(res))
	}
}

func TestUnitUpdateNotReadyCode(t *testing.T) {
	cs, _ := newClient(t)
	tk := create(t, cs, map[string]any{"level": "task", "title": "T", "body": "b"})
	todo := ok(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": tk["fingerprint"], "state": "TODO"})
	res := fails(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": todo["fingerprint"], "body": ""}, "not_ready")
	missing, _ := res.StructuredContent.(map[string]any)["missing"].([]any)
	if len(missing) != 1 || missing[0] != "description" {
		t.Errorf("missing = %v", missing)
	}
}

func TestTaskSetStateDescriptionMentionsReadiness(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	for _, want := range []string{"description", "acceptance criteria", "not_ready"} {
		if !strings.Contains(got["task_set_state"].Description, want) {
			t.Errorf("task_set_state description lacks %q", want)
		}
	}
	if d := got["unit_update"].Description; !strings.Contains(d, "TODO or IN_PROGRESS") {
		t.Errorf("unit_update description = %q", d)
	}
}
