package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/vecindex"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const legacyReason = "built before its configuration was recorded"

func staleLine(reasons string) string {
	return "semantic index: stale (" + reasons + `); semantic search is off; run "ralph index --rebuild"`
}

func ralphFile(name string) string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, ".ralph", name)
}

// buildIndex embeds the project's records into the named file with a fake provider.
func buildIndex(t *testing.T, name string, max int) {
	t.Helper()
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	idx, err := vecindex.Open(ralphFile(name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	s := embed.Settings{Provider: "ollama", Model: "nomic-embed-text", ChunkChars: 1200}
	if _, err := semantic.Sync(context.Background(), semantic.SyncDeps{DB: d, Index: idx, Provider: embedtest.NewFake(16), Settings: s}, semantic.SyncOptions{MaxRecords: max}); err != nil {
		t.Fatal(err)
	}
}

// makeStale builds the index, then removes its recorded configuration.
func makeStale(t *testing.T) *ollamatest.Server {
	t.Helper()
	srv := indexedProject(t)
	deleteRecordedConfig(t)
	return srv
}

func statusJSON(t *testing.T) map[string]any {
	t.Helper()
	code, out, stderr := run(t, "status", "--json")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStatusSemanticStaleLine(t *testing.T) {
	makeStale(t)
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "none")
	_, base, _ := run(t, "status")
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "ollama")
	code, out, stderr := run(t, "status")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if want := base + "\n" + staleLine(legacyReason) + "\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestStatusSemanticLineBeforeIntegrationLine(t *testing.T) {
	withFakeClaude(t)
	makeStale(t)
	makeOutdated(t, ".")
	_, out, _ := run(t, "status")
	if want := "\n" + staleLine(legacyReason) + "\n\n" + integrationOutdatedLine + "\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out = %q, want suffix %q", out, want)
	}
}

func TestStatusSemanticRebuildingLine(t *testing.T) {
	indexedProject(t)
	buildIndex(t, "index.rebuild.db", 2)
	_, base, _ := run(t, "status")
	if want := "\nsemantic index: rebuilding (2 of "; !strings.Contains(base, want) || !strings.HasSuffix(base, " records)\n") {
		t.Errorf("out = %q", base)
	}
}

func TestStatusSemanticSilentWhenOkEmptyOff(t *testing.T) {
	srv := indexedProject(t)
	_ = srv
	_, ok, _ := run(t, "status")
	if strings.Contains(ok, "semantic index") {
		t.Errorf("ok: %q", ok)
	}
	if err := os.Remove(ralphFile("index.db")); err != nil {
		t.Fatal(err)
	}
	_, empty, _ := run(t, "status")
	if strings.Contains(empty, "semantic index") {
		t.Errorf("empty: %q", empty)
	}
	if _, err := os.Stat(ralphFile("index.db")); err == nil {
		t.Error("status created the index file")
	}
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "none")
	_, off, _ := run(t, "status")
	if strings.Contains(off, "semantic index") {
		t.Errorf("off: %q", off)
	}
}

func TestStatusSemanticJSON(t *testing.T) {
	makeStale(t)
	idx, ok := statusJSON(t)["semantic_index"].(map[string]any)
	if !ok || idx["state"] != "stale" || idx["done"] != nil || idx["total"] != nil {
		t.Fatalf("stale: %v", idx)
	}
	if r, _ := idx["reasons"].([]any); len(r) != 1 || r[0] != legacyReason {
		t.Errorf("reasons = %v", idx["reasons"])
	}

	if err := os.Remove(ralphFile("index.db")); err != nil {
		t.Fatal(err)
	}
	buildIndex(t, "index.rebuild.db", 1)
	idx, _ = statusJSON(t)["semantic_index"].(map[string]any)
	if idx["state"] != "rebuilding" || idx["done"] != float64(1) || idx["total"] == nil || idx["reasons"] != nil {
		t.Errorf("rebuilding: %v", idx)
	}

	if err := os.Remove(ralphFile("index.rebuild.db")); err != nil {
		t.Fatal(err)
	}
	if idx, _ = statusJSON(t)["semantic_index"].(map[string]any); idx["state"] != "empty" {
		t.Errorf("empty: %v", idx)
	}

	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "none")
	if v := statusJSON(t); v["semantic_index"] != nil || v["briefs"] == nil {
		t.Errorf("off: %v", v)
	}
}

func TestStatusSemanticFailureDoesNotFailCommand(t *testing.T) {
	indexedProject(t)
	_, base, _ := run(t, "status")
	for _, s := range []string{"-wal", "-shm"} {
		_ = os.Remove(ralphFile("index.db" + s))
	}
	if err := os.WriteFile(ralphFile("index.db"), []byte(strings.Repeat("not a database ", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := run(t, "status")
	if code != 0 || out != base || !strings.Contains(stderr, "semantic index") {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestMonitorSemanticStaleLine(t *testing.T) {
	monitorEnv(t)
	_, before, _ := run(t, "monitor", "--no-poll")
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	buildIndex(t, "index.db", 0)
	t.Setenv("RALPH_EMBEDDINGS_MODEL", "other-model")
	code, out, stderr := run(t, "monitor", "--no-poll")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	i := strings.Index(out, "\nsemantic index: stale (")
	if i < 0 {
		t.Fatalf("no line:\n%s", out)
	}
	line := out[i+1 : i+1+strings.Index(out[i+1:], "\n")]
	if !strings.HasSuffix(line, `); semantic search is off; run "ralph index --rebuild"`) {
		t.Errorf("line = %q", line)
	}
	footer := out[i+1+len(line)+1:]
	if footer != "nothing to do\n" {
		t.Errorf("the line is not before the footer:\n%s", out)
	}
	if strings.Contains(before, "semantic index") {
		t.Errorf("line without an index:\n%s", before)
	}
}

func TestMonitorSemanticJSON(t *testing.T) {
	monitorEnv(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	buildIndex(t, "index.db", 0)
	t.Setenv("RALPH_EMBEDDINGS_MODEL", "other-model")
	code, out, stderr := run(t, "monitor", "--no-poll", "--json")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	idx, _ := v["semantic_index"].(map[string]any)
	if idx["state"] != "stale" || v["briefs"] == nil || v["followups"] == nil {
		t.Errorf("view = %v", v)
	}
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "none")
	_, out, _ = run(t, "monitor", "--no-poll", "--json")
	v = nil
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if _, has := v["semantic_index"]; has {
		t.Errorf("key present when off: %v", v)
	}
}

func noticeSource(t *testing.T) mcpserver.NoticeSource {
	t.Helper()
	a := loadedApp(t)
	return semanticNotice(a, func(ctx context.Context) (*db.DB, error) {
		return openStore(ctx, a, db.Options{MustExist: true})
	})
}

func TestSemanticNoticeStale(t *testing.T) {
	makeStale(t)
	got, err := noticeSource(t)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := `Semantic search is off: the index was built with a different embedding configuration (` + legacyReason +
		`). Rebuilding re-embeds every record and can take a while; ask the user whether to run "ralph index --rebuild --yes".`
	if len(got) != 1 || got[0].Code != "semantic_index_stale" || got[0].Message != want {
		t.Errorf("got %+v, want %q", got, want)
	}
}

func TestSemanticNoticeNoneWhenOkOrOffOrRebuilding(t *testing.T) {
	indexedProject(t)
	check := func(label string) {
		t.Helper()
		if got, err := noticeSource(t)(context.Background()); err != nil || len(got) != 0 {
			t.Errorf("%s: %+v, %v", label, got, err)
		}
	}
	check("ok")
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "none")
	deleteRecordedConfig(t)
	check("off")
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "ollama")
	buildIndex(t, "index.rebuild.db", 1)
	check("rebuilding")
}

func TestSemanticNoticeCachesByFileStat(t *testing.T) {
	makeStale(t)
	src := noticeSource(t)
	if got, err := src(context.Background()); err != nil || len(got) != 1 {
		t.Fatalf("first: %+v, %v", got, err)
	}
	path := ralphFile("index.db")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fi.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got, err := src(context.Background()); err != nil || len(got) != 1 {
		t.Errorf("unchanged file was reopened: %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := src(context.Background()); err == nil {
		t.Error("a changed file was not reopened")
	}
}

func TestSemanticNoticeLongReasonsTruncated(t *testing.T) {
	reasons := []string{strings.Repeat("model a -> model b; ", 60)}
	msg := semanticNoticeMessage(reasons)
	if n := len([]rune(msg)); n > 500 {
		t.Errorf("message has %d characters", n)
	}
	if !strings.Contains(msg, "…). Rebuilding") || !strings.HasSuffix(msg, `ask the user whether to run "ralph index --rebuild --yes".`) {
		t.Errorf("message = %q", msg)
	}
	if short := semanticNoticeMessage([]string{"a"}); strings.Contains(short, "…") {
		t.Errorf("short message cut: %q", short)
	}
}

func semanticNotices(t *testing.T, res map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	list, _ := res["notices"].([]any)
	for _, n := range list {
		m, _ := n.(map[string]any)
		out = append(out, m)
	}
	return out
}

func TestMCPSemanticNoticeSentOncePerSession(t *testing.T) {
	makeStale(t)
	cs := mcpSession(t)
	first := mcpCall(t, cs, "status", nil)
	m, _ := first.StructuredContent.(map[string]any)
	got := semanticNotices(t, m)
	if len(got) != 1 || got[0]["code"] != "semantic_index_stale" {
		t.Fatalf("first call notices = %v", got)
	}
	var text string
	for _, c := range first.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "Notice for the user: Semantic search is off") {
		t.Errorf("no notice text in %q", text)
	}
	m, _ = mcpCall(t, cs, "status", nil).StructuredContent.(map[string]any)
	if got := semanticNotices(t, m); len(got) != 0 {
		t.Errorf("second call notices = %v", got)
	}
}

func TestMCPSemanticNoticeUsesNoNetwork(t *testing.T) {
	srv := makeStale(t)
	before := len(srv.Requests())
	cs := mcpSession(t)
	m, _ := mcpCall(t, cs, "status", nil).StructuredContent.(map[string]any)
	if len(semanticNotices(t, m)) != 1 {
		t.Fatalf("notices = %v", m["notices"])
	}
	if after := len(srv.Requests()); after != before {
		t.Errorf("%d requests reached the provider", after-before)
	}
}
