package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var docToolNames = []string{"doc_create", "doc_get", "doc_list", "doc_update", "doc_archive", "doc_supersede", "doc_restore", "doc_versions"}

func staticDocs(svc documents.Service) func(context.Context) (documents.Service, error) {
	return func(context.Context) (documents.Service, error) { return svc, nil }
}

func newDocClient(t *testing.T) (*mcp.ClientSession, documents.Service) {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	svc := documents.NewService(d)
	opts := Options{Name: "ralph", Actor: actor, Open: staticOpen(units.NewService(d)), Documents: staticDocs(svc)}
	return connect(t, opts), svc
}

func docCreate(t *testing.T, cs *mcp.ClientSession, kind, title string, extra map[string]any) map[string]any {
	t.Helper()
	args := map[string]any{"kind": kind, "title": title, "body": "body of " + title}
	for k, v := range extra {
		args[k] = v
	}
	return ok(t, cs, "doc_create", args)
}

func TestDocToolsListed(t *testing.T) {
	cs, _ := newDocClient(t)
	got := tools(t, cs)
	readOnly := map[string]bool{"doc_get": true, "doc_list": true, "doc_versions": true}
	for _, name := range docToolNames {
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

func TestDocToolSchemasEnumerate(t *testing.T) {
	cs, _ := newDocClient(t)
	got := tools(t, cs)
	kinds := []string{"architecture", "design", "decision", "reference"}
	statuses := []string{"ACTIVE", "ARCHIVED", "SUPERSEDED"}
	for _, name := range []string{"doc_create", "doc_update"} {
		if g := enumStrings(schemaOf(t, got[name]).Properties["kind"]); !slices.Equal(g, kinds) {
			t.Errorf("%s kind enum = %v", name, g)
		}
	}
	list := schemaOf(t, got["doc_list"])
	if g := enumStrings(list.Properties["kinds"].Items); !slices.Equal(g, kinds) {
		t.Errorf("doc_list kinds enum = %v", g)
	}
	if g := enumStrings(list.Properties["statuses"].Items); !slices.Equal(g, statuses) {
		t.Errorf("doc_list statuses enum = %v", g)
	}
}

func TestDocToolSchemasRequireFingerprint(t *testing.T) {
	cs, _ := newDocClient(t)
	got := tools(t, cs)
	want := map[string][]string{
		"doc_update":    {"id", "fingerprint"},
		"doc_archive":   {"id", "fingerprint"},
		"doc_restore":   {"id", "fingerprint"},
		"doc_supersede": {"id", "fingerprint", "superseded_by"},
		"doc_create":    {"kind", "title", "body"},
	}
	for name, req := range want {
		have := schemaOf(t, got[name]).Required
		for _, r := range req {
			if !slices.Contains(have, r) {
				t.Errorf("%s does not require %s (required: %v)", name, r, have)
			}
		}
	}
	if slices.Contains(schemaOf(t, got["doc_create"]).Required, "brief_id") {
		t.Error("doc_create requires brief_id")
	}
}

func TestDocToolsAbsentWithoutOption(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(newSvc(t))})
	got := tools(t, cs)
	for _, name := range docToolNames {
		if _, found := got[name]; found {
			t.Errorf("tool %s listed without Options.Documents", name)
		}
	}
}

func TestDocDescriptionsMentionEssentials(t *testing.T) {
	cs, _ := newDocClient(t)
	got := tools(t, cs)
	create := got["doc_create"].Description
	for _, want := range []string{"architecture", "design", "decision", "reference", "brief", "fingerprint"} {
		if !strings.Contains(create, want) {
			t.Errorf("doc_create description lacks %q", want)
		}
	}
	if !strings.Contains(got["doc_supersede"].Description, "replace") {
		t.Error("doc_supersede description does not mention the replacement")
	}
	if !strings.Contains(got["doc_update"].Description, "ACTIVE") {
		t.Error("doc_update description does not say only active documents can be edited")
	}
}

func TestDocCreateProjectWideAndTied(t *testing.T) {
	cs, _ := newDocClient(t)
	brief := create(t, cs, map[string]any{"level": "brief", "title": "b", "body": "words"})
	wide := docCreate(t, cs, "architecture", "Wide", nil)
	if wide["id"] != "D-1" || wide["brief_id"] != "" || wide["status"] != "ACTIVE" || wide["fingerprint"] == "" || wide["version"] != float64(1) {
		t.Errorf("wide = %#v", wide)
	}
	tied := docCreate(t, cs, "decision", "Tied", map[string]any{"brief_id": brief["id"]})
	if tied["brief_id"] != brief["id"] || tied["fingerprint"] == "" {
		t.Errorf("tied = %#v", tied)
	}
}

func TestDocCreateTiedToSliceInvalid(t *testing.T) {
	cs, _ := newDocClient(t)
	brief := create(t, cs, map[string]any{"level": "brief", "title": "b", "body": "words"})
	slice := create(t, cs, map[string]any{"level": "slice", "parent_id": brief["id"], "title": "s"})
	fails(t, cs, "doc_create", map[string]any{"kind": "design", "title": "t", "body": "b", "brief_id": slice["id"]}, "invalid")
}

func TestDocCreateValidation(t *testing.T) {
	cs, _ := newDocClient(t)
	fails(t, cs, "doc_create", map[string]any{"kind": "design", "title": "t", "body": "  \n"}, "invalid")
	res := call(t, cs, "doc_create", map[string]any{"kind": "essay", "title": "t", "body": "b"})
	if !res.IsError {
		t.Error("a kind outside the enum was accepted")
	}
}

func TestDocGetWithAndWithoutVersion(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "One", nil)
	upd := ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "body": "second"})

	cur := ok(t, cs, "doc_get", map[string]any{"id": "d-1"})
	if cur["body"] != "second" || cur["fingerprint"] != upd["fingerprint"] || cur["status"] != "ACTIVE" {
		t.Errorf("current = %#v", cur)
	}
	if _, has := cur["current"]; has {
		t.Errorf("document form has current: %#v", cur)
	}

	old := ok(t, cs, "doc_get", map[string]any{"id": "D-1", "version": 1})
	if old["body"] != "body of One" || old["current"] != false || old["version"] != float64(1) || old["created_by"] != actor {
		t.Errorf("old = %#v", old)
	}
	if _, has := old["fingerprint"]; has {
		t.Errorf("version form has a fingerprint: %#v", old)
	}
	now := ok(t, cs, "doc_get", map[string]any{"id": "D-1", "version": 2})
	if now["current"] != true || now["body"] != "second" {
		t.Errorf("now = %#v", now)
	}
	if _, has := now["fingerprint"]; has {
		t.Errorf("current version form has a fingerprint: %#v", now)
	}
	fails(t, cs, "doc_get", map[string]any{"id": "D-1", "version": 9}, "not_found")
	fails(t, cs, "doc_get", map[string]any{"id": "D-1", "version": 0}, "invalid")
}

func TestDocGetNotFoundAndBadID(t *testing.T) {
	cs, _ := newDocClient(t)
	fails(t, cs, "doc_get", map[string]any{"id": "D-99"}, "not_found")
	fails(t, cs, "doc_get", map[string]any{"id": "T-1"}, "invalid")
	fails(t, cs, "doc_get", map[string]any{"id": "nonsense"}, "invalid")
}

func listIDs(t *testing.T, cs *mcp.ClientSession, args map[string]any) []string {
	t.Helper()
	res := ok(t, cs, "doc_list", args)
	var ids []string
	for _, d := range res["documents"].([]any) {
		ids = append(ids, d.(map[string]any)["id"].(string))
	}
	return ids
}

func TestDocListDefaultsToActive(t *testing.T) {
	cs, _ := newDocClient(t)
	a := docCreate(t, cs, "design", "A", nil)
	docCreate(t, cs, "design", "B", nil)
	ok(t, cs, "doc_archive", map[string]any{"id": a["id"], "fingerprint": a["fingerprint"]})
	if got := listIDs(t, cs, nil); !slices.Equal(got, []string{"D-2"}) {
		t.Errorf("default list = %v", got)
	}
	if got := listIDs(t, cs, map[string]any{"statuses": []string{"ARCHIVED"}}); !slices.Equal(got, []string{"D-1"}) {
		t.Errorf("archived list = %v", got)
	}
}

func TestDocListFilters(t *testing.T) {
	cs, _ := newDocClient(t)
	brief := create(t, cs, map[string]any{"level": "brief", "title": "b", "body": "words"})
	docCreate(t, cs, "architecture", "wide arch", nil)
	docCreate(t, cs, "decision", "tied decision", map[string]any{"brief_id": brief["id"]})
	docCreate(t, cs, "design", "wide design", nil)
	repl := docCreate(t, cs, "design", "replacement", nil)
	old := docCreate(t, cs, "reference", "old", nil)
	ok(t, cs, "doc_supersede", map[string]any{"id": old["id"], "fingerprint": old["fingerprint"], "superseded_by": repl["id"]})

	cases := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"brief", map[string]any{"brief_id": brief["id"]}, []string{"D-2"}},
		{"project only", map[string]any{"project_only": true}, []string{"D-1", "D-3", "D-4"}},
		{"kinds", map[string]any{"kinds": []string{"design", "decision"}}, []string{"D-2", "D-3", "D-4"}},
		{"superseded", map[string]any{"statuses": []string{"SUPERSEDED"}}, []string{"D-5"}},
		{"all statuses", map[string]any{"statuses": []string{"ACTIVE", "SUPERSEDED"}, "project_only": true, "kinds": []string{"design", "reference"}}, []string{"D-3", "D-4", "D-5"}},
	}
	for _, c := range cases {
		if got := listIDs(t, cs, c.args); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	res := ok(t, cs, "doc_list", map[string]any{"statuses": []string{"SUPERSEDED"}})
	first := res["documents"].([]any)[0].(map[string]any)
	if first["superseded_by"] != repl["id"] {
		t.Errorf("superseded_by = %v", first["superseded_by"])
	}
}

func TestDocListProjectOnlyWithBriefInvalid(t *testing.T) {
	cs, _ := newDocClient(t)
	fails(t, cs, "doc_list", map[string]any{"project_only": true, "brief_id": "B-1"}, "invalid")
}

func TestDocListHasNoBodies(t *testing.T) {
	cs, _ := newDocClient(t)
	docCreate(t, cs, "design", "A", nil)
	res := ok(t, cs, "doc_list", nil)
	d := res["documents"].([]any)[0].(map[string]any)
	if _, has := d["body"]; has {
		t.Errorf("summary has a body: %#v", d)
	}
	for _, k := range []string{"id", "kind", "brief_id", "title", "status", "superseded_by", "version", "updated_at", "fingerprint"} {
		if _, has := d[k]; !has {
			t.Errorf("summary lacks %s: %#v", k, d)
		}
	}
}

func TestDocUpdateChangesFields(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "Old", nil)
	upd := ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "title": "New", "kind": "decision"})
	if upd["title"] != "New" || upd["kind"] != "decision" || upd["body"] != "body of Old" || upd["version"] != float64(2) {
		t.Errorf("updated = %#v", upd)
	}
	if upd["fingerprint"] == d["fingerprint"] {
		t.Error("fingerprint did not change")
	}
	same := ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": upd["fingerprint"], "title": "New"})
	if same["version"] != float64(2) {
		t.Errorf("no-op edit made version %v", same["version"])
	}
}

type countingDocs struct {
	documents.Service
	calls atomic.Int32
}

func (c *countingDocs) Update(ctx context.Context, id, fp string, ch documents.Change) (documents.Document, error) {
	c.calls.Add(1)
	return c.Service.Update(ctx, id, fp, ch)
}

func TestDocUpdateMissingFingerprintRejectedBySchema(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	fake := &countingDocs{Service: documents.NewService(d)}
	cs := connect(t, Options{Name: "ralph", Actor: actor, Documents: staticDocs(fake)})
	made := docCreate(t, cs, "design", "A", nil)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "doc_update", Arguments: map[string]any{"id": made["id"], "title": "x"}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatal("a call without a fingerprint was accepted")
	}
	if n := fake.calls.Load(); n != 0 {
		t.Errorf("service Update called %d times", n)
	}
}

func TestDocUpdateStaleNamesDocGet(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "A", nil)
	ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "title": "B"})
	res := fails(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "title": "C"}, "stale")
	msg := text(res)
	if !strings.Contains(msg, "doc_get") || strings.Contains(msg, "unit_get") {
		t.Errorf("stale text = %q", msg)
	}
	for _, name := range []string{"doc_archive", "doc_restore"} {
		r := fails(t, cs, name, map[string]any{"id": d["id"], "fingerprint": d["fingerprint"]}, "stale")
		if !strings.Contains(text(r), "doc_get") {
			t.Errorf("%s stale text = %q", name, text(r))
		}
	}
}

func TestDocUpdateInactiveInvalid(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "A", nil)
	arch := ok(t, cs, "doc_archive", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"]})
	res := fails(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": arch["fingerprint"], "title": "B"}, "invalid")
	if !strings.Contains(text(res), "restore") {
		t.Errorf("text = %q", text(res))
	}
}

func TestDocArchiveSupersedeRestoreRoundTrip(t *testing.T) {
	cs, _ := newDocClient(t)
	a := docCreate(t, cs, "design", "A", nil)
	b := docCreate(t, cs, "design", "B", nil)

	arch := ok(t, cs, "doc_archive", map[string]any{"id": a["id"], "fingerprint": a["fingerprint"]})
	if arch["status"] != "ARCHIVED" || arch["fingerprint"] == a["fingerprint"] {
		t.Errorf("archived = %#v", arch)
	}
	rest := ok(t, cs, "doc_restore", map[string]any{"id": a["id"], "fingerprint": arch["fingerprint"]})
	if rest["status"] != "ACTIVE" {
		t.Errorf("restored = %#v", rest)
	}
	sup := ok(t, cs, "doc_supersede", map[string]any{"id": a["id"], "fingerprint": rest["fingerprint"], "superseded_by": b["id"]})
	if sup["status"] != "SUPERSEDED" || sup["superseded_by"] != b["id"] {
		t.Errorf("superseded = %#v", sup)
	}
	back := ok(t, cs, "doc_restore", map[string]any{"id": a["id"], "fingerprint": sup["fingerprint"]})
	if back["status"] != "ACTIVE" || back["superseded_by"] != "" || back["version"] != float64(1) {
		t.Errorf("restored again = %#v", back)
	}
	fails(t, cs, "doc_restore", map[string]any{"id": a["id"], "fingerprint": back["fingerprint"]}, "invalid")
}

func TestDocSupersedeRequiresReplacement(t *testing.T) {
	cs, _ := newDocClient(t)
	a := docCreate(t, cs, "design", "A", nil)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "doc_supersede", Arguments: map[string]any{"id": a["id"], "fingerprint": a["fingerprint"]}})
	if err == nil && !res.IsError {
		t.Error("supersede without superseded_by was accepted")
	}
	fails(t, cs, "doc_supersede", map[string]any{"id": a["id"], "fingerprint": a["fingerprint"], "superseded_by": a["id"]}, "invalid")
	fails(t, cs, "doc_supersede", map[string]any{"id": a["id"], "fingerprint": a["fingerprint"], "superseded_by": "D-99"}, "not_found")
}

func TestDocVersionsListsEarlierVersions(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "One", nil)
	u2 := ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "title": "Two"})
	ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": u2["fingerprint"], "body": "three"})

	res := ok(t, cs, "doc_versions", map[string]any{"id": d["id"]})
	if res["current"] != float64(3) {
		t.Errorf("current = %v", res["current"])
	}
	vs := res["versions"].([]any)
	if len(vs) != 2 {
		t.Fatalf("versions = %#v", vs)
	}
	first := vs[0].(map[string]any)
	if first["version"] != float64(1) || first["title"] != "One" || first["size"] != float64(len("body of One")) || first["created_by"] != actor {
		t.Errorf("first = %#v", first)
	}
	for _, v := range vs {
		if _, has := v.(map[string]any)["body"]; has {
			t.Errorf("version has a body: %#v", v)
		}
	}
	fails(t, cs, "doc_versions", map[string]any{"id": "D-99"}, "not_found")
}

func TestDocWritesCarryActor(t *testing.T) {
	cs, _ := newDocClient(t)
	d := docCreate(t, cs, "design", "A", nil)
	if d["created_by"] != actor || d["updated_by"] != actor {
		t.Errorf("created = %#v", d)
	}
	upd := ok(t, cs, "doc_update", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"], "title": "B"})
	if upd["updated_by"] != actor {
		t.Errorf("updated = %#v", upd)
	}
}

func TestDocFactoryLazyAndCached(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	svc := documents.NewService(d)
	var calls atomic.Int32
	failFirst := true
	cs := connect(t, Options{Name: "ralph", Actor: actor, Documents: func(context.Context) (documents.Service, error) {
		calls.Add(1)
		if failFirst {
			failFirst = false
			return nil, errors.New("boom")
		}
		return svc, nil
	}})
	if n := calls.Load(); n != 0 {
		t.Fatalf("factory called %d times before any call", n)
	}
	fails(t, cs, "doc_list", nil, "internal")
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls = %d after the failure", n)
	}
	ok(t, cs, "doc_list", nil)
	ok(t, cs, "doc_list", nil)
	if n := calls.Load(); n != 2 {
		t.Errorf("calls = %d, want the failure plus one success", n)
	}
}

func TestDocFactoryNotInitialized(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Documents: func(context.Context) (documents.Service, error) {
		return nil, fmt.Errorf("open: %w", db.ErrNotFound)
	}})
	res := fails(t, cs, "doc_list", nil, "not_initialized")
	if !strings.Contains(text(res), "ralph init") {
		t.Errorf("text = %q", text(res))
	}
}

func TestDocResultsCarryNotices(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Documents: staticDocs(documents.NewService(d)),
		Sources: []NoticeSource{func(context.Context) ([]Notice, error) {
			return []Notice{{Code: "hello", Message: "tell the user"}}, nil
		}},
	})
	res := ok(t, cs, "doc_list", nil)
	ns, _ := res["notices"].([]any)
	if len(ns) != 1 || ns[0].(map[string]any)["code"] != "hello" {
		t.Errorf("notices = %#v", res["notices"])
	}
	if again := ok(t, cs, "doc_list", nil); len(again["notices"].([]any)) != 0 {
		t.Errorf("notice repeated: %#v", again["notices"])
	}
}
