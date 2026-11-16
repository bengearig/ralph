package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type e2eHit struct {
	Kind, ID, Title, Snippet, Status string
	Inactive                         bool
}

type e2eFound struct {
	Relaxed bool
	Hits    []e2eHit
	Raw     map[string]any
}

func e2eSearchWith(t *testing.T, cs *mcp.ClientSession, args map[string]any) e2eFound {
	t.Helper()
	raw := e2eOK(t, cs, "search", args)
	found := e2eFound{Raw: raw}
	found.Relaxed, _ = raw["relaxed"].(bool)
	hits, _ := raw["hits"].([]any)
	for _, h := range hits {
		m := h.(map[string]any)
		hit := e2eHit{}
		hit.Kind, _ = m["kind"].(string)
		hit.ID, _ = m["id"].(string)
		hit.Title, _ = m["title"].(string)
		hit.Snippet, _ = m["snippet"].(string)
		hit.Status, _ = m["status"].(string)
		hit.Inactive, _ = m["inactive"].(bool)
		found.Hits = append(found.Hits, hit)
	}
	return found
}

func e2eSearch(t *testing.T, cs *mcp.ClientSession, query string) e2eFound {
	t.Helper()
	return e2eSearchWith(t, cs, map[string]any{"query": query})
}

func (f e2eFound) keys() []string {
	out := []string{}
	for _, h := range f.Hits {
		out = append(out, h.Kind+":"+h.ID)
	}
	return out
}

func e2eWantHits(t *testing.T, label string, got e2eFound, want ...string) {
	t.Helper()
	if strings.Join(got.keys(), ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", label, got.keys(), want)
	}
}

func e2eWantHit(t *testing.T, label string, f e2eFound, kind, id, title, marked string) {
	t.Helper()
	if len(f.Hits) == 0 {
		t.Errorf("%s: no hits", label)
		return
	}
	h := f.Hits[0]
	if h.Kind != kind || h.ID != id || h.Title != title {
		t.Errorf("%s: first hit = %s %s %q, want %s %s %q", label, h.Kind, h.ID, h.Title, kind, id, title)
	}
	if !strings.Contains(strings.ToLower(h.Snippet), "["+marked+"]") {
		t.Errorf("%s: snippet %q does not mark %q", label, h.Snippet, marked)
	}
}

type searchData struct {
	brief, slice, task, wide, tied, broker string
}

func e2eSearchData(t *testing.T, cs *mcp.ClientSession) searchData {
	t.Helper()
	var d searchData
	d.brief = e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Dashboard launch", "body": "Ship the dashboard with dark theme support."})["id"].(string)
	d.slice = e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": d.brief, "title": "Widgets slice", "body": "Build the widget gallery."})["id"].(string)
	d.task = e2eOK(t, cs, "unit_create", map[string]any{
		"level": "task", "parent_id": d.slice, "title": "Render charts",
		"body": "Draw the charts using canvas.", "acceptance_criteria": "Axis labels remain legible.",
	})["id"].(string)
	e2eOK(t, cs, "note_add", map[string]any{"id": d.task, "body": "Consider a zebrafish pattern for tooltips."})
	d.wide = e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "System overview", "body": "The gateway routes requests through the relay."})["id"].(string)
	d.tied = e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick storage", "body": "We choose sqlite for persistence.", "brief_id": d.brief})["id"].(string)
	d.broker = e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Relay notes", "body": "The sqlite engine and the persistence layer."})["id"].(string)
	return d
}

func TestE2ESearchFlow(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := e2eSearchData(t, cs)

	e2eWantHit(t, "brief body", e2eSearch(t, cs, "dark"), "brief", d.brief, "Dashboard launch", "dark")
	e2eWantHit(t, "slice body", e2eSearch(t, cs, "gallery"), "slice", d.slice, "Widgets slice", "gallery")
	e2eWantHit(t, "task body", e2eSearch(t, cs, "canvas"), "task", d.task, "Render charts", "canvas")
	e2eWantHit(t, "task criteria", e2eSearch(t, cs, "legible"), "task", d.task, "Render charts", "legible")
	e2eWantHit(t, "wide document", e2eSearch(t, cs, "gateway"), "document", d.wide, "System overview", "gateway")
	e2eWantHit(t, "tied document", e2eSearch(t, cs, "choose"), "document", d.tied, "Pick storage", "choose")
	e2eWantHit(t, "note", e2eSearch(t, cs, "zebrafish"), "note", d.task, "Render charts", "zebrafish")
	e2eWantHits(t, "criteria only", e2eSearch(t, cs, "legible"), "task:"+d.task)

	if f := e2eSearch(t, cs, "themes"); len(f.Hits) == 0 || f.Hits[0].ID != d.brief {
		t.Errorf("stemmed themes = %v, want the brief first", f.keys())
	}

	title := e2eSearch(t, cs, "relay")
	e2eWantHits(t, "title above body", title, "document:"+d.broker, "document:"+d.wide)
	if f := e2eSearch(t, cs, "relay"); f.Relaxed {
		t.Error("relay was relaxed")
	}

	both := e2eSearch(t, cs, "sqlite persistence")
	if both.Relaxed || len(both.Hits) != 2 {
		t.Errorf("sqlite persistence = %v relaxed %v", both.keys(), both.Relaxed)
	}
	phrase := e2eSearch(t, cs, `"choose sqlite"`)
	e2eWantHits(t, "phrase", phrase, "document:"+d.tied)

	relaxed := e2eSearch(t, cs, "gateway canvas")
	if !relaxed.Relaxed {
		t.Error("gateway canvas was not relaxed")
	}
	got := map[string]bool{}
	for _, k := range relaxed.keys() {
		got[k] = true
	}
	if !got["document:"+d.wide] || !got["task:"+d.task] {
		t.Errorf("relaxed hits = %v", relaxed.keys())
	}

	res := e2eCall(t, cs, "search", map[string]any{"query": "!!! ???"})
	if code := e2eErrorCode(t, res); code != "invalid" {
		t.Errorf("punctuation only: error_code = %q (%s)", code, e2eText(res))
	}
	for _, q := range []string{`title:foo OR "bar`, `a AND (b`, "NEAR(x", `foo*`, "-x"} {
		if r := e2eCall(t, cs, "search", map[string]any{"query": q}); r.IsError {
			t.Errorf("query %q raised an error: %s", q, e2eText(r))
		}
	}

	e2eWantHits(t, "kinds", e2eSearchWith(t, cs, map[string]any{"query": "sqlite", "kinds": []string{"note"}}))
}

func TestE2ESearchTracksChanges(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := e2eSearchData(t, cs)
	doc := d.wide
	key := "document:" + doc

	cur := e2eOK(t, cs, "doc_get", map[string]any{"id": doc})
	upd := e2eOK(t, cs, "doc_update", map[string]any{"id": doc, "fingerprint": cur["fingerprint"], "body": "The harbour handles quokka traffic."})
	e2eWantHits(t, "new words", e2eSearch(t, cs, "quokka"), key)
	e2eWantHits(t, "old words", e2eSearch(t, cs, "gateway"))
	e2eWantHits(t, "old words with inactive", e2eSearchWith(t, cs, map[string]any{"query": "gateway", "include_inactive": true}))
	old := e2eOK(t, cs, "doc_get", map[string]any{"id": doc, "version": 1})
	if !strings.Contains(old["body"].(string), "gateway") {
		t.Errorf("version 1 body = %v", old["body"])
	}
	e2eWantVersions(t, cs, doc, 1)

	arch := e2eOK(t, cs, "doc_archive", map[string]any{"id": doc, "fingerprint": upd["fingerprint"]})
	e2eWantHits(t, "archived", e2eSearch(t, cs, "quokka"))
	all := e2eSearchWith(t, cs, map[string]any{"query": "quokka", "include_inactive": true})
	e2eWantHits(t, "archived with inactive", all, key)
	if !all.Hits[0].Inactive {
		t.Errorf("archived hit not marked inactive: %v", all.Hits[0])
	}

	restored := e2eOK(t, cs, "doc_restore", map[string]any{"id": doc, "fingerprint": arch["fingerprint"]})
	back := e2eSearch(t, cs, "quokka")
	e2eWantHits(t, "restored", back, key)
	if back.Hits[0].Inactive {
		t.Error("restored hit still inactive")
	}

	e2eOK(t, cs, "doc_supersede", map[string]any{"id": doc, "fingerprint": restored["fingerprint"], "superseded_by": d.tied})
	e2eWantHits(t, "superseded", e2eSearch(t, cs, "quokka"))
	sup := e2eSearchWith(t, cs, map[string]any{"query": "quokka", "include_inactive": true})
	e2eWantHits(t, "superseded with inactive", sup, key)
	if !sup.Hits[0].Inactive {
		t.Errorf("superseded hit not marked inactive: %v", sup.Hits[0])
	}

	task := e2eOK(t, cs, "unit_get", map[string]any{"id": d.task})
	e2eOK(t, cs, "task_set_state", map[string]any{"id": d.task, "fingerprint": task["fingerprint"], "state": "CANCELLED", "reason": "obsolete requirement"})
	for _, q := range []string{"canvas", "legible", "zebrafish", "obsolete"} {
		e2eWantHits(t, "cancelled "+q, e2eSearch(t, cs, q))
		f := e2eSearchWith(t, cs, map[string]any{"query": q, "include_inactive": true})
		if len(f.Hits) != 1 || f.Hits[0].ID != d.task || !f.Hits[0].Inactive {
			t.Errorf("cancelled %s with inactive = %v %v", q, f.keys(), f.Hits)
		}
	}
	if f := e2eSearchWith(t, cs, map[string]any{"query": "zebrafish", "include_inactive": true}); len(f.Hits) == 1 && f.Hits[0].Kind != "note" {
		t.Errorf("zebrafish kind = %s, want note", f.Hits[0].Kind)
	}
}

func TestE2ESearchCommand(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := e2eSearchData(t, cs)

	r := e.run(t, "search", "gateway")
	if r.code != 0 {
		t.Fatalf("search exit %d: %s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if len(lines) != 2 || lines[0] != d.wide+"  document  ACTIVE  System overview" || !strings.HasPrefix(lines[1], "    ") || !strings.Contains(lines[1], "[gateway]") {
		t.Errorf("search gateway output = %q", r.stdout)
	}

	r = e.run(t, "search", "zebrafish")
	if want := d.task + "  note  PROPOSED  Render charts\n"; !strings.HasPrefix(r.stdout, want) {
		t.Errorf("note output = %q, want prefix %q", r.stdout, want)
	}

	r = e.run(t, "search", "gateway", "canvas")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "no result had every word; these match some of them:\n") {
		t.Errorf("relaxed output = %q (exit %d)", r.stdout, r.code)
	}

	r = e.run(t, "search", "nonexistentword")
	if r.code != 0 || r.stdout != "no results\n" {
		t.Errorf("no results output = %q (exit %d)", r.stdout, r.code)
	}

	r = e.run(t, "search", "sqlite", "--kind", "document")
	if r.code != 0 || strings.Count(r.stdout, "  document  ") != 2 {
		t.Errorf("--kind document = %q", r.stdout)
	}
	r = e.run(t, "search", "sqlite", "--kind", "task")
	if r.stdout != "no results\n" {
		t.Errorf("--kind task = %q", r.stdout)
	}
	r = e.run(t, "search", "sqlite", "--kind", "bogus")
	if r.code != 1 || !strings.Contains(r.stderr, "--kind") {
		t.Errorf("--kind bogus: exit %d, stderr %q", r.code, r.stderr)
	}

	r = e.run(t, "search", "sqlite", "--limit", "1")
	if r.code != 0 || strings.Count(r.stdout, "  document  ") != 1 {
		t.Errorf("--limit 1 = %q", r.stdout)
	}

	task := e2eOK(t, cs, "unit_get", map[string]any{"id": d.task})
	e2eOK(t, cs, "task_set_state", map[string]any{"id": d.task, "fingerprint": task["fingerprint"], "state": "CANCELLED", "reason": "dropped"})
	r = e.run(t, "search", "canvas")
	if r.stdout != "no results\n" {
		t.Errorf("cancelled task without --all = %q", r.stdout)
	}
	r = e.run(t, "search", "canvas", "--all")
	if r.code != 0 || !strings.HasPrefix(r.stdout, d.task+"  task  CANCELLED  Render charts (inactive)\n") {
		t.Errorf("--all output = %q", r.stdout)
	}

	for _, args := range [][]string{
		{"gateway"},
		{"gateway", "canvas"},
		{"sqlite", "--kind", "document", "--limit", "1"},
		{"canvas", "--all"},
		{"nonexistentword"},
	} {
		query := map[string]any{"query": strings.Join(args, " ")}
		cli := append([]string{"search", "--json"}, args...)
		for i, a := range args {
			switch a {
			case "--kind":
				query["query"] = strings.Join(args[:i], " ")
				query["kinds"] = []string{args[i+1]}
			case "--limit":
				query["limit"] = 1
			case "--all":
				query["query"] = strings.Join(args[:i], " ")
				query["include_inactive"] = true
			}
		}
		if len(args) > 1 && args[1] == "canvas" {
			query["query"] = "gateway canvas"
		}
		jr := e.run(t, cli...)
		if jr.code != 0 {
			t.Fatalf("search %v: exit %d: %s", cli, jr.code, jr.stderr)
		}
		var fromCLI map[string]any
		if err := json.Unmarshal([]byte(jr.stdout), &fromCLI); err != nil {
			t.Fatalf("search %v: %v\n%s", cli, err, jr.stdout)
		}
		fromTool := e2eOK(t, cs, "search", query)
		delete(fromTool, "notices")
		if !reflect.DeepEqual(fromCLI, fromTool) {
			t.Errorf("search %v: --json = %v, tool = %v", args, fromCLI, fromTool)
		}
	}

	r = e.run(t, "search")
	if r.code != 1 {
		t.Errorf("search with no words: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestE2ESearchSurvivesPurgeAndCompaction(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := e2eSearchData(t, cs)

	for _, id := range []string{d.wide, d.tied, d.broker} {
		fp := e2eOK(t, cs, "doc_get", map[string]any{"id": id})["fingerprint"].(string)
		for i := 1; i <= 4; i++ {
			fp = e2eOK(t, cs, "doc_update", map[string]any{"id": id, "fingerprint": fp, "body": fmt.Sprintf("%s edit%d keepword", id, i)})["fingerprint"].(string)
		}
		e2eWantVersions(t, cs, id, 1, 2, 3, 4)
	}
	titles := map[string]string{d.wide: "System overview", d.tied: "Pick storage", d.broker: "Relay notes"}

	r := e.run(t, "docs", "purge", "--keep", "0", "--yes")
	if r.code != 0 {
		t.Fatalf("purge exit %d: %s %s", r.code, r.stdout, r.stderr)
	}
	for _, id := range []string{d.wide, d.tied, d.broker} {
		e2eWantVersions(t, cs, id)
		var found *e2eHit
		for _, h := range e2eSearch(t, cs, "keepword").Hits {
			if h.ID == id {
				found = &h
			}
		}
		if found == nil || found.Kind != "document" || found.Title != titles[id] {
			t.Errorf("%s after purge = %+v", id, found)
		}
		if f := e2eSearch(t, cs, "edit1"); len(f.Hits) != 0 {
			t.Errorf("earlier version found after purge: %v", f.keys())
		}
	}
	e2eWantHit(t, "task after purge", e2eSearch(t, cs, "canvas"), "task", d.task, "Render charts", "canvas")
	e2eWantHit(t, "note after purge", e2eSearch(t, cs, "zebrafish"), "note", d.task, "Render charts", "zebrafish")
	e2eWantHit(t, "brief after purge", e2eSearch(t, cs, "dark"), "brief", d.brief, "Dashboard launch", "dark")

	r = e.run(t, "search", "keepword")
	if r.code != 0 || strings.Count(r.stdout, "  document  ACTIVE  ") != 3 {
		t.Errorf("command after purge = %q", r.stdout)
	}
}

func TestE2ESearchUpgradeBackfill(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)

	store := project.StorePath(e.dir)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(store + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	old, err := db.Open(context.Background(), store, db.Options{Migrations: db.Migrations[:7], Mode: db.MigrateAuto})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`INSERT INTO units (id, level, number, title, body, created_at, updated_at) VALUES ('B-1', 'brief', 1, 'Legacy brief', 'Old darkness theme.', 'x', 'x')`,
		`INSERT INTO units (id, level, number, parent_id, position, title, body, created_at, updated_at) VALUES ('S-1', 'slice', 1, 'B-1', 1, 'Legacy slice', 'Old gallery.', 'x', 'x')`,
		`INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES ('T-1', 'task', 1, 'S-1', 1, 'Legacy task', 'Old canvas.', 'Old legible labels.', 'TODO', 'x', 'x')`,
		`INSERT INTO notes (unit_id, kind, body, created_at) VALUES ('T-1', 'NOTE', 'Old zebrafish remark.', 'x')`,
		`INSERT INTO documents (id, number, kind, title, body, created_at, updated_at) VALUES ('D-1', 1, 'design', 'Legacy design', 'Old gateway text.', 'x', 'x')`,
		`INSERT INTO counters (level, last) VALUES ('brief', 1), ('slice', 1), ('task', 1), ('document', 1) ON CONFLICT (level) DO UPDATE SET last = excluded.last`,
	}
	for _, s := range stmts {
		if _, err := old.SQL().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	var n int
	if err := old.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'search_fts'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("old database already has search_fts (%d, %v)", n, err)
	}
	if _, err := old.SQL().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	cs := e.connect(t, io.Discard)
	e2eWantHit(t, "legacy brief", e2eSearch(t, cs, "darkness"), "brief", "B-1", "Legacy brief", "darkness")
	e2eWantHit(t, "legacy slice", e2eSearch(t, cs, "gallery"), "slice", "S-1", "Legacy slice", "gallery")
	e2eWantHit(t, "legacy task", e2eSearch(t, cs, "canvas"), "task", "T-1", "Legacy task", "canvas")
	e2eWantHit(t, "legacy criteria", e2eSearch(t, cs, "legible"), "task", "T-1", "Legacy task", "legible")
	e2eWantHit(t, "legacy note", e2eSearch(t, cs, "zebrafish"), "note", "T-1", "Legacy task", "zebrafish")
	e2eWantHit(t, "legacy document", e2eSearch(t, cs, "gateway"), "document", "D-1", "Legacy design", "gateway")

	doc := e2eOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Fresh", "body": "Brand new quokka."})
	e2eWantHits(t, "new after upgrade", e2eSearch(t, cs, "quokka"), "document:"+doc["id"].(string))

	r := e.run(t, "search", "gateway")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "D-1  document  ACTIVE  Legacy design") {
		t.Errorf("command after upgrade = %q (exit %d, %s)", r.stdout, r.code, r.stderr)
	}
}
