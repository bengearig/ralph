package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// linkedProject holds a brief B-1, a slice S-1 and a task T-1, a project-wide
// document D-1 and a document D-2 tied to the brief.
func linkedProject(t *testing.T) (*e2eEnv, *mcp.ClientSession) {
	t.Helper()
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Ship it", "body": "Please ship it."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "First slice"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-1", "title": "First task", "body": "do it"})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "Overview", "body": "The shape."})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick a Store", "body": "SQLite.", "brief_id": "B-1"})
	return e, cs
}

func (e *e2eEnv) ralphOut(t *testing.T, args ...string) string {
	t.Helper()
	return e.wantRun(t, 0, args...).stdout
}

func linkedIDs(t *testing.T, got map[string]any, field string) []string {
	t.Helper()
	list, _ := got[field].([]any)
	var ids []string
	for _, item := range list {
		m, _ := item.(map[string]any)
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestE2EDocLinksFlow(t *testing.T) {
	e, cs := linkedProject(t)
	before := e.ralphOut(t, "status")

	e2eOK(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "d-1"})
	e2eOK(t, cs, "link_add", map[string]any{"id": "S-1", "kind": "document", "target": "d-2"})

	task := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if ids := linkedIDs(t, task, "documents"); len(ids) != 1 || ids[0] != "D-1" {
		t.Errorf("documents = %v, want [D-1]", task["documents"])
	}
	inherited, _ := task["inherited_documents"].([]any)
	if len(inherited) != 1 {
		t.Fatalf("inherited_documents = %v, want one", task["inherited_documents"])
	}
	if m, _ := inherited[0].(map[string]any); m["id"] != "D-2" || m["from"] != "S-1" {
		t.Errorf("inherited_documents[0] = %v, want D-2 from S-1", inherited[0])
	}

	want := "Documents:\n" +
		"D-1  architecture  ACTIVE  Overview\n" +
		"Documents from S-1:\n" +
		"D-2  decision  ACTIVE  Pick a Store\n"
	if out := e.ralphOut(t, "status", "T-1"); !strings.Contains(out, want) {
		t.Errorf("ralph status T-1 lacks\n%s\nin:\n%s", want, out)
	}

	if out := e.ralphOut(t, "docs", "links", "D-1"); !strings.Contains(out, "T-1") || !strings.Contains(out, "task") || !strings.Contains(out, "First task") {
		t.Errorf("ralph docs links D-1:\n%s", out)
	}
	doc := e2eOK(t, cs, "doc_get", map[string]any{"id": "D-1"})
	units, _ := doc["linked_units"].([]any)
	if len(units) != 1 {
		t.Fatalf("D-1 linked_units = %v", doc["linked_units"])
	}
	if m, _ := units[0].(map[string]any); m["id"] != "T-1" || m["level"] != "task" || m["title"] != "First task" {
		t.Errorf("D-1 linked_units[0] = %v", units[0])
	}

	brief := e.ralphOut(t, "docs", "links", "B-1")
	if strings.Contains(brief, "D-2") {
		t.Errorf("the brief tie made D-2 a link of B-1:\n%s", brief)
	}
	if units, _ := e2eOK(t, cs, "doc_get", map[string]any{"id": "D-2"})["linked_units"].([]any); len(units) != 1 {
		t.Errorf("D-2 linked_units = %v, want only the slice", units)
	}
	e.wantRun(t, 0, "docs", "link", "D-2", "B-1")
	if out := e.ralphOut(t, "docs", "links", "B-1"); !strings.Contains(out, "D-2") {
		t.Errorf("after ralph docs link D-2 B-1:\n%s", out)
	}
	if units, _ := e2eOK(t, cs, "doc_get", map[string]any{"id": "D-2"})["linked_units"].([]any); len(units) != 2 {
		t.Errorf("D-2 linked_units = %v, want the brief and the slice", units)
	}

	if after := e.ralphOut(t, "status"); after != before {
		t.Errorf("the status overview changed with links:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestE2EDocLinksRules(t *testing.T) {
	e, cs := linkedProject(t)
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Old notes", "body": "old"})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Replaced notes", "body": "replaced"})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "New notes", "body": "new"})

	if res := e2eCall(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-99"}); e2eErrorCode(t, res) != "not_found" {
		t.Errorf("missing document: %s", e2eText(res))
	}

	e2eDocStatus(t, cs, "doc_archive", "D-3", nil)
	if res := e2eCall(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-3"}); e2eErrorCode(t, res) != "invalid" {
		t.Errorf("archived document: %s", e2eText(res))
	}

	e2eDocStatus(t, cs, "doc_supersede", "D-4", map[string]any{"superseded_by": "D-5"})
	res := e2eCall(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-4"})
	if e2eErrorCode(t, res) != "invalid" || !strings.Contains(e2eText(res), "D-5") {
		t.Errorf("superseded document: %q", e2eText(res))
	}

	ext := e2eOK(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "external", "target": "PROJ-12"})
	if links, _ := ext["links"].([]any); len(links) != 1 {
		t.Errorf("external link_add = %v", ext)
	}

	// The CLI refuses what the tools refuse.
	for _, doc := range []string{"D-99", "D-3", "D-4"} {
		if r := e.run(t, "docs", "link", doc, "T-1"); r.code == 0 {
			t.Errorf("ralph docs link %s T-1 succeeded: %s", doc, r.stdout)
		}
	}

	e2eOK(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-1"})
	e.wantRun(t, 0, "docs", "link", "D-2", "T-1")
	toolDocs := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if ids := linkedIDs(t, toolDocs, "documents"); strings.Join(ids, ",") != "D-1,D-2" {
		t.Errorf("tool documents = %v", ids)
	}
	if out := e.ralphOut(t, "docs", "links", "T-1"); !strings.Contains(out, "D-1  architecture  ACTIVE  Overview") || !strings.Contains(out, "D-2  decision  ACTIVE  Pick a Store") {
		t.Errorf("CLI and tools disagree:\n%s", out)
	}

	e2eDocStatus(t, cs, "doc_archive", "D-1", nil)
	if out := e.ralphOut(t, "status", "T-1"); !strings.Contains(out, "D-1  architecture  ARCHIVED  Overview") {
		t.Errorf("archived link missing from status:\n%s", out)
	}
	task := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if docs, _ := task["documents"].([]any); len(docs) != 2 {
		t.Errorf("documents after archive = %v", task["documents"])
	} else if m, _ := docs[0].(map[string]any); m["id"] != "D-1" || m["status"] != "ARCHIVED" {
		t.Errorf("documents[0] after archive = %v", docs[0])
	}

	e.wantRun(t, 0, "docs", "unlink", "D-1", "T-1")
	e.wantRun(t, 0, "docs", "unlink", "D-1", "T-1")
	task = e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if ids := linkedIDs(t, task, "documents"); strings.Join(ids, ",") != "D-2" {
		t.Errorf("documents after unlink = %v", ids)
	}
	e2eOK(t, cs, "link_remove", map[string]any{"id": "T-1", "kind": "document", "target": "D-1"})
}

func TestE2EDocLinksSupersededShowsReplacement(t *testing.T) {
	e, cs := linkedProject(t)
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "Overview v2", "body": "The new shape."})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "Overview v3", "body": "The newest shape."})
	e2eOK(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-1"})
	e2eDocStatus(t, cs, "doc_supersede", "D-1", map[string]any{"superseded_by": "D-3"})

	out := e.ralphOut(t, "status", "T-1")
	if !strings.Contains(out, "D-1  architecture  SUPERSEDED (replaced by D-3)  Overview") {
		t.Errorf("ralph status T-1:\n%s", out)
	}
	text := e2eText(e2eCall(t, cs, "unit_get", map[string]any{"id": "T-1"}))
	if !strings.Contains(text, "document: D-1 architecture SUPERSEDED by D-3 Overview") {
		t.Errorf("unit_get text:\n%s", text)
	}
	// Nothing follows the replacement: D-3 is not added, nor whatever replaces it later.
	task := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if ids := linkedIDs(t, task, "documents"); strings.Join(ids, ",") != "D-1" {
		t.Errorf("documents = %v, want only D-1", ids)
	}
	if strings.Contains(out, "D-3  ") || strings.Contains(text, "document: D-3") {
		t.Errorf("the replacement was followed:\n%s\n%s", out, text)
	}
	if out := e.ralphOut(t, "docs", "links", "D-3"); !strings.Contains(out, "no units are linked to D-3") {
		t.Errorf("ralph docs links D-3:\n%s", out)
	}
}

func TestE2EDocLinksSurviveRestart(t *testing.T) {
	e, cs := linkedProject(t)
	e2eOK(t, cs, "link_add", map[string]any{"id": "T-1", "kind": "document", "target": "D-1"})
	e2eOK(t, cs, "link_add", map[string]any{"id": "S-1", "kind": "document", "target": "D-2"})
	if err := cs.Close(); err != nil {
		t.Logf("close first session: %v", err)
	}

	second := e.connect(t, io.Discard)
	task := e2eOK(t, second, "unit_get", map[string]any{"id": "T-1"})
	if ids := linkedIDs(t, task, "documents"); strings.Join(ids, ",") != "D-1" {
		t.Errorf("documents in the second process = %v", ids)
	}
	if ids := linkedIDs(t, task, "inherited_documents"); strings.Join(ids, ",") != "D-2" {
		t.Errorf("inherited_documents in the second process = %v", ids)
	}
	if units, _ := e2eOK(t, second, "doc_get", map[string]any{"id": "D-1"})["linked_units"].([]any); len(units) != 1 {
		t.Errorf("linked_units in the second process = %v", units)
	}
	if out := e.ralphOut(t, "docs", "links", "T-1"); !strings.Contains(out, "D-1") || !strings.Contains(out, "D-2") {
		t.Errorf("ralph docs links T-1:\n%s", out)
	}
	if out := e.ralphOut(t, "docs", "links", "D-2"); !strings.Contains(out, "S-1") {
		t.Errorf("ralph docs links D-2:\n%s", out)
	}
}

func TestE2EPlanningInstructionsMentionDocuments(t *testing.T) {
	read := func(e *e2eEnv, rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(e.dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct {
		harness string
		prefix  string
		cmdDir  string
		notes   string
		bare    bool
	}{
		{"claude-code", "mcp__ralph__", ".claude/commands/ralph", "CLAUDE.md", false},
		{"opencode", "", ".opencode/commands/ralph", "AGENTS.md", true},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			e := newE2EEnv(t)
			e.wantRun(t, 0, "init", "--harness", c.harness)
			tools := map[string][]string{
				"receive":    {"search", "doc_create", "doc_update", "link_add"},
				"articulate": {"search", "doc_get", "doc_create", "link_add"},
			}
			for name, list := range tools {
				content := read(e, filepath.Join(c.cmdDir, name+".md"))
				for _, tool := range list {
					if !strings.Contains(content, "`"+c.prefix+tool+"`") {
						t.Errorf("%s command lacks `%s%s`", name, c.prefix, tool)
					}
				}
				if c.bare && (strings.Contains(content, "mcp__") || strings.Contains(content, "ralph_")) {
					t.Errorf("%s command has a tool prefix", name)
				}
			}
			notes := read(e, c.notes)
			for _, want := range []string{"`search`", "`doc_`", "`link_add`", "kind `document`"} {
				if !strings.Contains(notes, want) {
					t.Errorf("%s lacks %s", c.notes, want)
				}
			}
		})
	}
}
