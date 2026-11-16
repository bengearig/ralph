package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/testutil"
)

type searchFixture struct {
	doc, task string
}

// seedSearchProject fills an initialized project through the MCP tools, then
// closes the session so the command opens the database alone.
func seedSearchProject(t *testing.T) searchFixture {
	t.Helper()
	newStatusEnv(t)
	cs := mcpSession(t)
	var f searchFixture
	f.doc = mcpOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Lemur habitat", "body": "enclosure design"})["id"].(string)
	brief := mcpOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Zoo brief", "body": "words"})["id"].(string)
	slice := mcpOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": brief, "title": "Zoo slice"})["id"].(string)
	f.task = mcpOK(t, cs, "unit_create", map[string]any{
		"level": "task", "parent_id": slice, "title": "Feed the lemur", "body": "fruit twice daily", "acceptance_criteria": "fed",
	})["id"].(string)
	mcpOK(t, cs, "note_add", map[string]any{"id": f.task, "body": "the aardvark refuses fruit"})
	_ = cs.Close()
	return f
}

func TestSearchCommandFindsRecords(t *testing.T) {
	f := seedSearchProject(t)

	code, out, stderr := run(t, "search", "enclosure")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || lines[0] != f.doc+"  document  ACTIVE  Lemur habitat" || !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("document output = %q", out)
	}
	if !strings.Contains(lines[1], "[enclosure]") {
		t.Errorf("snippet lacks the bracketed match: %q", lines[1])
	}

	_, out, _ = run(t, "search", "--kind", "task", "fruit")
	lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], f.task+"  task  ") || !strings.HasSuffix(lines[0], "  Feed the lemur") {
		t.Errorf("task output = %q", out)
	}
	if !strings.HasPrefix(lines[1], "    ") || !strings.Contains(lines[1], "[fruit]") {
		t.Errorf("task snippet = %q", lines[1])
	}

	_, out, _ = run(t, "search", "aardvark")
	lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], f.task+"  note  ") || !strings.HasSuffix(lines[0], "  Feed the lemur") || !strings.Contains(lines[1], "[aardvark]") {
		t.Errorf("note output = %q", out)
	}
}

func TestSearchCommandRelaxedHeader(t *testing.T) {
	seedSearchProject(t)
	code, out, _ := run(t, "search", "lemur", "narwhal")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if first, _, _ := strings.Cut(out, "\n"); first != "no result had every word; these match some of them:" {
		t.Errorf("output = %q", out)
	}
}

func TestSearchCommandNoResults(t *testing.T) {
	seedSearchProject(t)
	code, out, _ := run(t, "search", "narwhal")
	if code != 0 || out != "no results\n" {
		t.Errorf("code %d output %q", code, out)
	}
}

func TestSearchCommandKindFlagRepeatable(t *testing.T) {
	f := seedSearchProject(t)
	_, out, _ := run(t, "search", "--kind", "task", "--kind", "document", "lemur")
	if !strings.Contains(out, f.doc+"  document") || !strings.Contains(out, f.task+"  task") || strings.Contains(out, "  brief  ") || strings.Contains(out, "  slice  ") {
		t.Errorf("output = %q", out)
	}
	_, out, _ = run(t, "search", "--kind", "document", "lemur")
	if strings.Contains(out, "  task  ") {
		t.Errorf("output = %q", out)
	}
}

func TestSearchCommandLimit(t *testing.T) {
	seedSearchProject(t)
	_, out, _ := run(t, "search", "--limit", "1", "--json", "lemur")
	var r search.Result
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Hits) != 1 {
		t.Errorf("err %v hits %d", err, len(r.Hits))
	}
}

func TestSearchCommandAllIncludesInactive(t *testing.T) {
	f := seedSearchProject(t)
	cs := mcpSession(t)
	doc := mcpOK(t, cs, "doc_get", map[string]any{"id": f.doc})
	mcpOK(t, cs, "doc_archive", map[string]any{"id": f.doc, "fingerprint": doc["fingerprint"]})
	_ = cs.Close()

	_, out, _ := run(t, "search", "enclosure")
	if out != "no results\n" {
		t.Errorf("default output = %q", out)
	}
	_, out, _ = run(t, "search", "--all", "enclosure")
	if first, _, _ := strings.Cut(out, "\n"); first != f.doc+"  document  ARCHIVED  Lemur habitat (inactive)" {
		t.Errorf("--all output = %q", out)
	}
}

func TestSearchCommandJSON(t *testing.T) {
	seedSearchProject(t)
	code, out, _ := run(t, "search", "--json", "lemur")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	var fromCLI search.Result
	if err := json.Unmarshal([]byte(out), &fromCLI); err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t)
	structured := mcpOK(t, cs, "search", map[string]any{"query": "lemur"})
	delete(structured, "notices")
	raw, _ := json.Marshal(structured)
	var fromTool search.Result
	if err := json.Unmarshal(raw, &fromTool); err != nil {
		t.Fatal(err)
	}
	if len(fromCLI.Hits) == 0 || !reflect.DeepEqual(fromCLI, fromTool) {
		t.Errorf("cli %+v != tool %+v", fromCLI, fromTool)
	}

	_, out, _ = run(t, "search", "--json", "narwhal")
	if !strings.Contains(out, `"hits": []`) && !strings.Contains(out, `"hits":[]`) {
		t.Errorf("empty json = %q", out)
	}
}

func TestSearchCommandNeedsWords(t *testing.T) {
	seedSearchProject(t)
	code, out, stderr := run(t, "search")
	if code != 1 || out != "" || !strings.Contains(stderr, "requires at least 1 arg") {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestSearchCommandInvalidQuery(t *testing.T) {
	seedSearchProject(t)
	code, out, stderr := run(t, "search", "!!!")
	if code != 1 || out != "" || !strings.HasPrefix(stderr, "ralph: ") || !strings.Contains(stderr, "invalid") {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestSearchCommandBadKind(t *testing.T) {
	seedSearchProject(t)
	code, _, stderr := run(t, "search", "--kind", "folder", "x")
	if code != 1 || !strings.Contains(stderr, "brief, slice, task, note, document") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}

func TestSearchCommandBadLimit(t *testing.T) {
	seedSearchProject(t)
	for _, l := range []string{"0", "101", "-1"} {
		code, _, stderr := run(t, "search", "--limit", l, "x")
		if code != 1 || !strings.Contains(stderr, "1 to 100") {
			t.Errorf("limit %s: code %d stderr %q", l, code, stderr)
		}
	}
}

func TestSearchCommandNotInitialized(t *testing.T) {
	inRepo(t)
	code, out, stderr := run(t, "search", "x")
	if want := "ralph: " + errNoDatabase + "\n"; code != 1 || out != "" || stderr != want {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestSearchCommandOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "search", "x")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}

func TestMCPSearchSharesDatabase(t *testing.T) {
	newStatusEnv(t)
	cs := mcpSession(t)
	doc := mcpOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick", "body": "chinchilla dust baths"})["id"].(string)
	task := mcpOK(t, cs, "unit_create", map[string]any{"level": "task", "title": "Bathe chinchilla", "body": "dust", "acceptance_criteria": "clean"})["id"].(string)
	out := mcpOK(t, cs, "search", map[string]any{"query": "chinchilla"})
	got := map[string]bool{}
	for _, h := range out["hits"].([]any) {
		got[h.(map[string]any)["id"].(string)] = true
	}
	if !got[doc] || !got[task] {
		t.Errorf("hits = %v, want %s and %s", out["hits"], doc, task)
	}
}
