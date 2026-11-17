package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/vecindex"
)

const (
	modelA = "nomic-embed-text"
	modelB = "other-embed"

	// modelBReason is what differs offline: the second model has no built-in prefixes.
	modelBReason = "model " + modelA + " -> " + modelB + `; document prefix "search_document: " -> ""; query prefix "search_query: " -> ""`
)

// staleFront forwards to a stub, can delay and fail embedding requests and counts the ones it let through.
type staleFront struct {
	srv       *httptest.Server
	failAfter atomic.Int64
	embeds    atomic.Int64
}

func newStaleFront(t *testing.T, stub *ollamatest.Server, delay time.Duration) *staleFront {
	t.Helper()
	target, err := url.Parse(stub.URL())
	if err != nil {
		t.Fatal(err)
	}
	f := &staleFront{}
	f.failAfter.Store(-1)
	proxy := httputil.NewSingleHostReverseProxy(target)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			n := f.embeds.Add(1)
			if limit := f.failAfter.Load(); limit >= 0 && n > limit {
				f.embeds.Add(-1)
				http.Error(w, `{"error":"stub failure"}`, http.StatusServiceUnavailable)
				return
			}
			time.Sleep(delay)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// useModel points the user-level configuration at the service with the model and extra embeddings settings.
func (e *e2eEnv) useModel(t *testing.T, serviceURL, model string, extra ...string) {
	t.Helper()
	yaml := "embeddings:\n  provider: ollama\n  url: " + serviceURL + "\n  model: " + model + "\n"
	for _, l := range extra {
		yaml += "  " + l + "\n"
	}
	e.writeUserConfig(t, yaml)
}

// staleE2EProject seeds records and indexes them with modelA; the stub also serves modelB.
func staleE2EProject(t *testing.T) (*e2eEnv, *ollamatest.Server, semanticData) {
	t.Helper()
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := semanticSeed(t, cs)
	_ = cs.Close()
	srv := ollamatest.NewServer(t, ollamatest.Options{Models: []string{modelA, modelB}})
	e.useModel(t, srv.URL(), modelA)
	e.wantIndexed(t, "indexed 10 records")
	return e, srv, d
}

func (e *e2eEnv) keywordHits(t *testing.T, args ...string) []string {
	t.Helper()
	saved := e.env
	e.env = append(slices.Clone(saved), "RALPH_EMBEDDINGS_PROVIDER=none")
	defer func() { e.env = saved }()
	return hitIDs(e.searchJSON(t, args...))
}

func countPath(srv *ollamatest.Server, path string) (n int) {
	for _, r := range srv.Requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

func (e *e2eEnv) wantStale(t *testing.T, label, reason string) search.Result {
	t.Helper()
	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeKeywordFallback || !strings.Contains(res.Notice, "semantic search is off") || !strings.Contains(res.Notice, reason) || !strings.Contains(res.Notice, `ralph index --rebuild`) {
		t.Errorf("%s: mode %q notice %q, want stale for %q", label, res.Mode, res.Notice, reason)
	}
	return res
}

func (e *e2eEnv) wantNotStale(t *testing.T, label string) {
	t.Helper()
	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Errorf("%s: mode %q notice %q, want hybrid and no notice", label, res.Mode, res.Notice)
	}
}

// startRun starts a ralph command and returns a channel of its stdout lines and a function that waits for it.
func (e *e2eEnv) startRun(t *testing.T, args ...string) (lines <-chan string, wait func() error, cmd *exec.Cmd) {
	t.Helper()
	cmd = e.command(context.Background(), args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ch := make(chan string, 100)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(ch)
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()
	return ch, func() error {
		<-done
		return cmd.Wait()
	}, cmd
}

// waitLine reads lines until one contains want.
func waitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the command ended before it printed %q", want)
			}
			if strings.Contains(l, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no line with %q", want)
		}
	}
}

func TestE2EStaleIndexModelChange(t *testing.T) {
	e, srv, _ := staleE2EProject(t)
	e.wantNotStale(t, "before the change")
	e.useModel(t, srv.URL(), modelB)
	embeds := countPath(srv, "/api/embed")

	reason := "model " + modelA + " -> " + modelB
	res := e.wantStale(t, "json", reason)
	if want := e.keywordHits(t, "gateway"); !slices.Equal(hitIDs(res), want) || len(want) == 0 {
		t.Errorf("fallback hits %v, keyword hits %v", hitIDs(res), want)
	}
	text := e.run(t, "search", "gateway")
	first := strings.SplitN(text.stdout, "\n", 2)[0]
	if text.code != 0 || !strings.HasPrefix(first, "note: ") || !strings.Contains(first, reason) || !strings.Contains(first, `ralph index --rebuild`) {
		t.Errorf("text search: exit %d out %q", text.code, text.stdout)
	}

	r := e.run(t, "index")
	want := "ralph: the semantic index was built with a different embedding configuration (" + reason + "; digest sha256:nomic -> sha256:other; " + strings.TrimPrefix(modelBReason, reason+"; ") + `); run "ralph index --rebuild" to rebuild it` + "\n"
	if r.code != 1 || r.stdout != "" || r.stderr != want {
		t.Errorf("index: exit %d out %q err %q", r.code, r.stdout, r.stderr)
	}
	if got := countPath(srv, "/api/embed"); got != embeds {
		t.Errorf("a stale index embedded %d inputs", got-embeds)
	}

	st := e.run(t, "status")
	if line := `semantic index: stale (` + modelBReason + `); semantic search is off; run "ralph index --rebuild"`; st.code != 0 || !strings.Contains(st.stdout, line+"\n") {
		t.Errorf("status: exit %d out %q", st.code, st.stdout)
	}
	js := e.run(t, "status", "--json")
	var sj struct {
		SemanticIndex *semanticIndexJSON `json:"semantic_index"`
	}
	if err := json.Unmarshal([]byte(js.stdout), &sj); err != nil || js.code != 0 || sj.SemanticIndex == nil || sj.SemanticIndex.State != "stale" || len(sj.SemanticIndex.Reasons) == 0 || sj.SemanticIndex.Reasons[0] != reason {
		t.Errorf("status --json: exit %d err %v out %q", js.code, err, js.stdout)
	}
	is := e.run(t, "index", "--status")
	if is.code != 0 || !strings.HasSuffix(is.stdout, "state: stale ("+modelBReason+")\n") {
		t.Errorf("index --status: exit %d out %q", is.code, is.stdout)
	}
}

func TestE2EStaleIndexDigestChange(t *testing.T) {
	t.Run("found by a search", func(t *testing.T) {
		e, srv, _ := staleE2EProject(t)
		srv.AddModel(modelA, "sha256:replaced")
		tags := countPath(srv, "/api/tags")

		cs := e.connect(t, io.Discard)
		for i := 0; i < 2; i++ {
			res := e2eOK(t, cs, "search", map[string]any{"query": "gateway"})
			notice, _ := res["notice"].(string)
			if res["mode"] != search.ModeKeywordFallback || !strings.Contains(notice, "digest sha256:nomic -> sha256:repla") {
				t.Fatalf("search %d: mode %v notice %q", i, res["mode"], notice)
			}
		}
		if got := countPath(srv, "/api/tags") - tags; got != 1 {
			t.Errorf("one process made %d /api/tags requests, want 1", got)
		}
		_ = cs.Close()

		if st := e.run(t, "status"); !strings.Contains(st.stdout, "semantic index: stale (digest sha256:nomic -> sha256:repla)") {
			t.Errorf("status = %q", st.stdout)
		}
		if is := e.run(t, "index", "--status"); !strings.Contains(is.stdout, "state: stale (digest ") {
			t.Errorf("index --status = %q", is.stdout)
		}
	})

	t.Run("unreachable provider is not stale", func(t *testing.T) {
		e, srv, _ := staleE2EProject(t)
		srv.Close()
		res := e.searchJSON(t, "gateway")
		if res.Mode != search.ModeKeywordFallback || !strings.HasPrefix(res.Notice, "semantic search is unavailable (") || strings.Contains(res.Notice, "semantic search is off") {
			t.Errorf("mode %q notice %q", res.Mode, res.Notice)
		}
		if st := e.run(t, "status"); strings.Contains(st.stdout, "semantic index:") {
			t.Errorf("status reports a state with the provider down: %q", st.stdout)
		}
	})

	t.Run("saved finding is kept while the provider is down", func(t *testing.T) {
		e, srv, _ := staleE2EProject(t)
		srv.AddModel(modelA, "sha256:replaced")
		e.wantStale(t, "found", "digest")
		srv.Close()
		e.wantStale(t, "provider down", "digest")
		if st := e.run(t, "status"); !strings.Contains(st.stdout, "semantic index: stale (digest ") {
			t.Errorf("status = %q", st.stdout)
		}
	})
}

func TestE2EStaleIndexSettingsChange(t *testing.T) {
	e, srv, _ := staleE2EProject(t)
	other := ollamatest.NewServer(t, ollamatest.Options{Models: []string{modelA, modelB}})

	for _, c := range []struct{ name, setting, reason string }{
		{"chunk size", "chunk_chars: 900", "chunk size 1200 -> 900"},
		{"document prefix", `document_prefix: "doc: "`, `document prefix "search_document: " -> "doc: "`},
		{"query prefix", `query_prefix: "q: "`, `query prefix "search_query: " -> "q: "`},
	} {
		e.useModel(t, srv.URL(), modelA, c.setting)
		e.wantStale(t, c.name, c.reason)
		if is := e.run(t, "index", "--status"); !strings.Contains(is.stdout, "state: stale ("+c.reason+")") {
			t.Errorf("%s: index --status = %q", c.name, is.stdout)
		}
	}

	e.useModel(t, srv.URL(), modelA, `document_prefix: "search_document: "`, `query_prefix: "search_query: "`)
	e.wantNotStale(t, "the built-in prefixes written by hand")
	e.useModel(t, other.URL(), modelA)
	e.wantNotStale(t, "another service address with the same model")
	if st := e.run(t, "status"); strings.Contains(st.stdout, "semantic index:") {
		t.Errorf("status = %q", st.stdout)
	}
}

// e2eIndexContent describes what the index file holds: its records, chunks and recorded configuration.
func e2eIndexContent(t *testing.T, e *e2eEnv) string {
	t.Helper()
	idx, err := vecindex.Open(project.IndexPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	recs, err := idx.Records(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := idx.Meta(t.Context(), "config")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(recs, cfg)
}

func rebuildFiles(t *testing.T, e *e2eEnv) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(e.dir, ".ralph", project.IndexRebuildFile+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestE2ERebuildPlanThenYes(t *testing.T) {
	e, srv, d := staleE2EProject(t)
	e.useModel(t, srv.URL(), modelB)
	indexPath := project.IndexPath(e.dir)
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	content := e2eIndexContent(t, e)
	embeds := countPath(srv, "/api/embed")

	plan := e.run(t, "index", "--rebuild")
	reason := "model " + modelA + " -> " + modelB
	if plan.code != 0 || !strings.HasPrefix(plan.stdout, "rebuild plan\nreason: "+reason+"; ") || !strings.HasSuffix(plan.stdout, "run again with --yes to start\n") {
		t.Fatalf("plan: exit %d out %q err %q", plan.code, plan.stdout, plan.stderr)
	}
	if e2eIndexContent(t, e) != content {
		t.Error("the plan changed what the index holds")
	}
	if got := countPath(srv, "/api/embed"); got != embeds {
		t.Errorf("the plan embedded %d inputs", got-embeds)
	}
	if files := rebuildFiles(t, e); len(files) != 0 {
		t.Errorf("the plan left %v", files)
	}

	r := e.run(t, "index", "--rebuild", "--yes")
	if r.code != 0 || r.stderr != "" || !strings.HasSuffix(r.stdout, "rebuilt 10 records (10 chunks); semantic search now uses "+modelB+"\n") {
		t.Fatalf("rebuild: exit %d out %q err %q", r.code, r.stdout, r.stderr)
	}
	if got := countPath(srv, "/api/embed") - embeds; got != 10 {
		t.Errorf("the rebuild embedded %d inputs, want 10", got)
	}

	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeHybrid || res.Notice != "" || len(res.Hits) == 0 || res.Hits[0].ID != d.wide {
		t.Errorf("after the rebuild: mode %q notice %q hits %v", res.Mode, res.Notice, hitIDs(res))
	}
	is := e.run(t, "index", "--status")
	if !strings.Contains(is.stdout, "built with: "+modelB+" ") || !strings.HasSuffix(is.stdout, "state: ok\n") {
		t.Errorf("index --status = %q", is.stdout)
	}
	if st := e.run(t, "status"); st.code != 0 || strings.Contains(st.stdout, "semantic index:") {
		t.Errorf("status = %q", st.stdout)
	}
	if after, _ := os.ReadFile(indexPath); string(after) == string(before) {
		t.Error("the old index file content is still in place")
	}
	if files := rebuildFiles(t, e); len(files) != 0 {
		t.Errorf("files left after the rebuild: %v", files)
	}
	if r := e.run(t, "index"); r.code != 0 || !strings.Contains(r.stdout, "index is up to date") {
		t.Errorf("index after the rebuild: exit %d out %q", r.code, r.stdout)
	}
}

func TestE2ERebuildInterruptedResumes(t *testing.T) {
	e, srv, _ := staleE2EProject(t)
	front := newStaleFront(t, srv, 0)
	e.useModel(t, front.srv.URL, modelB)

	front.failAfter.Store(3)
	r := e.run(t, "index", "--rebuild", "--yes")
	if r.code != 1 || !strings.HasSuffix(r.stderr, "\nrun ralph index --rebuild --yes to continue\n") {
		t.Fatalf("failing run: exit %d out %q err %q", r.code, r.stdout, r.stderr)
	}
	if is := e.run(t, "index", "--status"); !strings.Contains(is.stdout, "built with: "+modelA+" ") || !strings.Contains(is.stdout, "records: 10 of 10 indexed") {
		t.Errorf("the old index changed while the rebuild failed: %q", is.stdout)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".ralph", project.IndexRebuildFile)); err != nil {
		t.Errorf("rebuild file: %v", err)
	}
	e.wantStale(t, "during an interrupted rebuild", "model "+modelA+" -> "+modelB)

	st := e.run(t, "status")
	m := regexp.MustCompile(`semantic index: rebuilding \((\d+) of 10 records\)`).FindStringSubmatch(st.stdout)
	if m == nil {
		t.Fatalf("status = %q", st.stdout)
	}
	done, _ := strconv.Atoi(m[1])
	if done < 1 || done > 3 {
		t.Errorf("done = %d, want between 1 and 3", done)
	}

	front.failAfter.Store(-1)
	embeds := countPath(srv, "/api/embed")
	r = e.run(t, "index", "--rebuild", "--yes")
	if r.code != 0 || !strings.Contains(r.stdout, "rebuilt 10 records") {
		t.Fatalf("resumed run: exit %d out %q err %q", r.code, r.stdout, r.stderr)
	}
	if got := countPath(srv, "/api/embed") - embeds; got != 10-done {
		t.Errorf("the resumed run embedded %d inputs, want %d", got, 10-done)
	}
	e.wantNotStale(t, "after the resumed rebuild")

	slow := newStaleFront(t, srv, 400*time.Millisecond)
	e.useModel(t, slow.srv.URL, modelB, "chunk_chars: 900")
	lines, wait, cmd := e.startRun(t, "index", "--rebuild", "--yes")
	waitLine(t, lines, "[1/")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := wait(); !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Errorf("interrupted run: %v, want exit 130", err)
	}
}

func TestE2ERebuildWhileMCPServerRuns(t *testing.T) {
	e, srv, d := staleE2EProject(t)
	e.useModel(t, srv.URL(), modelB)
	cs := e.connect(t, io.Discard)

	before := e2eOK(t, cs, "search", map[string]any{"query": "gateway"})
	notice, _ := before["notice"].(string)
	if before["mode"] != search.ModeKeywordFallback || !strings.Contains(notice, "model "+modelA+" -> "+modelB) {
		t.Fatalf("before the rebuild: mode %v notice %q", before["mode"], notice)
	}
	if hits, _ := before["hits"].([]any); len(hits) == 0 {
		t.Error("no keyword results before the rebuild")
	}
	if got := e2eOK(t, cs, "search", map[string]any{"query": d.query}); got["mode"] != search.ModeKeywordFallback {
		t.Errorf("semantic-only query before the rebuild: %v", got["mode"])
	}

	if r := e.run(t, "index", "--rebuild", "--yes"); r.code != 0 {
		t.Fatalf("rebuild: exit %d: %s", r.code, r.stderr)
	}

	after := e2eOK(t, cs, "search", map[string]any{"query": d.query})
	hits, _ := after["hits"].([]any)
	if after["mode"] != search.ModeHybrid || after["notice"] != nil || len(hits) == 0 {
		t.Fatalf("after the rebuild: %v", after)
	}
	if first, _ := hits[0].(map[string]any); first["id"] != d.target || fmt.Sprint(first["sources"]) != "[semantic]" {
		t.Errorf("first hit after the rebuild = %v", hits[0])
	}
	if res := e2eOK(t, cs, "search", map[string]any{"query": "gateway"}); res["mode"] != search.ModeHybrid {
		t.Errorf("same process, keyword query: mode %v", res["mode"])
	}
}

func TestE2EStaleNoticeInMCP(t *testing.T) {
	e, srv, _ := staleE2EProject(t)
	e.useModel(t, srv.URL(), modelB)
	requests := len(srv.Requests())
	want := semanticNoticePrefix + modelBReason + semanticNoticeSuffix

	cs := e.connect(t, io.Discard)
	first := e2eCall(t, cs, "status", nil)
	if first.IsError {
		t.Fatalf("status: %s", e2eText(first))
	}
	fm, _ := first.StructuredContent.(map[string]any)
	list, _ := fm["notices"].([]any)
	if len(list) != 1 {
		t.Fatalf("notices = %v", fm["notices"])
	}
	if n, _ := list[0].(map[string]any); n["code"] != "semantic_index_stale" || n["message"] != want {
		t.Errorf("notice = %v, want message %q", list[0], want)
	}
	if !strings.Contains(e2eText(first), "Notice for the user: "+want) {
		t.Errorf("text lacks the notice line: %q", e2eText(first))
	}
	second := e2eCall(t, cs, "status", nil)
	sm, _ := second.StructuredContent.(map[string]any)
	if again, _ := sm["notices"].([]any); len(again) != 0 || strings.Contains(e2eText(second), "Notice for the user") {
		t.Errorf("second call carries a notice: %v", sm["notices"])
	}
	if got := len(srv.Requests()); got != requests {
		t.Errorf("the notice cost %d requests to the stub", got-requests)
	}

	notes := e.readFile(t, filepath.Join(e.dir, "CLAUDE.md"))
	if !strings.Contains(notes, "A notice can ask for a decision: relay it, ask the user, and do nothing until they answer") {
		t.Error("CLAUDE.md lacks the sentence about asking before a decision")
	}
}

func TestE2EBusyLock(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	semanticSeed(t, cs)
	_ = cs.Close()
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	front := newStaleFront(t, srv, 400*time.Millisecond)
	e.useModel(t, front.srv.URL, modelA)

	lines, wait, _ := e.startRun(t, "index")
	waitLine(t, lines, "embedding ")

	second := e.run(t, "index")
	if second.code != 1 || !strings.Contains(second.stderr, "another ralph index run is in progress") {
		t.Errorf("second index: exit %d out %q err %q", second.code, second.stdout, second.stderr)
	}
	if res := e.searchJSON(t, "gateway"); len(res.Hits) == 0 {
		t.Errorf("search during the run: %+v", res)
	}
	for range lines {
	}
	if err := wait(); err != nil {
		t.Errorf("the first run: %v", err)
	}
}

func TestE2EStaleNothingWhenProviderNone(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	semanticSeed(t, cs)
	_ = cs.Close()
	e.writeUserConfig(t, "embeddings:\n  provider: none\n")

	for _, args := range [][]string{{"status"}, {"status", "--json"}} {
		r := e.run(t, args...)
		if r.code != 0 || strings.Contains(r.stdout, "semantic index") || strings.Contains(r.stdout, "semantic_index") {
			t.Errorf("%v: exit %d out %q", args, r.code, r.stdout)
		}
	}
	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeKeyword || res.Notice != "" {
		t.Errorf("mode %q notice %q", res.Mode, res.Notice)
	}
	cs = e.connect(t, io.Discard)
	got := e2eCall(t, cs, "status", nil)
	m, _ := got.StructuredContent.(map[string]any)
	if list, _ := m["notices"].([]any); len(list) != 0 || strings.Contains(e2eText(got), "semantic_index_stale") {
		t.Errorf("notices = %v", m["notices"])
	}
	if _, err := os.Stat(project.IndexPath(e.dir)); !os.IsNotExist(err) {
		t.Errorf("index file exists: %v", err)
	}
}
