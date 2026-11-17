package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/vecindex"
)

const (
	indexFile   = ".ralph/index.db"
	rebuildFile = ".ralph/index.rebuild.db"
)

// staleProject builds an index and then changes the model's digest so that the index is stale.
func staleProject(t *testing.T) *ollamatest.Server {
	t.Helper()
	srv := indexedProject(t)
	srv.AddModel("nomic-embed-text", "sha256:replaced")
	return srv
}

func rebuildPlanJSONOf(t *testing.T) rebuildPlanJSON {
	t.Helper()
	code, out, stderr := run(t, "index", "--rebuild", "--json")
	if code != 0 {
		t.Fatalf("plan: code %d stderr %q", code, stderr)
	}
	var p rebuildPlanJSON
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return p
}

// indexContent describes what the old index holds: its records, chunks and recorded configuration.
func indexContent(t *testing.T) string {
	t.Helper()
	idx, err := vecindex.Open(indexFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	recs, err := idx.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := idx.Meta(context.Background(), "config")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(recs, cfg)
}

func snapshot(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".ralph")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".ralph", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func TestIndexCommandRebuildWithoutYesPrintsPlanOnly(t *testing.T) {
	srv := staleProject(t)
	before, content, embeds := snapshot(t), indexContent(t), embedRequests(srv)
	code, out, stderr := run(t, "index", "--rebuild")
	want := "rebuild plan\n" +
		"reason: digest sha256:nomic -> sha256:repla\n" +
		"records: 5 (about 5 chunks)\n" +
		"model: nomic-embed-text at " + srv.URL() + "\n" +
		"the current index stays in use until the new one is complete\n" +
		"run again with --yes to start\n"
	if code != 0 || stderr != "" || out != want {
		t.Fatalf("code %d stderr %q\n%s", code, stderr, out)
	}
	if embedRequests(srv) != embeds {
		t.Error("the plan embedded something")
	}
	if indexContent(t) != content {
		t.Error("the index changed")
	}
	for name := range snapshot(t) {
		if _, ok := before[name]; !ok && !strings.HasPrefix(name, "index.db-") {
			t.Errorf("file %s created", name)
		}
	}
}

func TestIndexCommandRebuildPlanNoChangeReason(t *testing.T) {
	indexedProject(t)
	code, out, stderr := run(t, "index", "--rebuild")
	if code != 0 || stderr != "" || !strings.Contains(out, "\nreason: no change in configuration; a rebuild was asked for\n") {
		t.Fatalf("code %d stderr %q\n%s", code, stderr, out)
	}
}

func TestIndexCommandRebuildWithYesRebuilds(t *testing.T) {
	srv := staleProject(t)
	code, out, stderr := run(t, "index", "--rebuild", "--yes")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q\n%s", code, stderr, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("output %q", out)
	}
	for i, l := range lines[:5] {
		if prefix := fmt.Sprintf("[%d/5] embedding ", i+1); !strings.HasPrefix(l, prefix) || !strings.HasSuffix(l, " (1 chunks)") {
			t.Errorf("line %d = %q", i, l)
		}
	}
	if want := "rebuilt 5 records (5 chunks); semantic search now uses nomic-embed-text"; lines[5] != want {
		t.Errorf("last line %q", lines[5])
	}
	if code, out, stderr := run(t, "index"); code != 0 || !strings.Contains(out, "index is up to date") {
		t.Errorf("after: code %d stderr %q\n%s", code, stderr, out)
	}
	if _, err := os.Stat(rebuildFile); err == nil {
		t.Error("rebuild file left behind")
	}
	_ = srv
}

func TestIndexCommandRebuildBusyExit1(t *testing.T) {
	staleProject(t)
	release, err := semantic.LockIndex(".ralph")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	code, out, stderr := run(t, "index", "--rebuild", "--yes")
	if code != 1 || out != "" || stderr != "ralph: another ralph index run is in progress\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIndexCommandRebuildProviderDownExit1(t *testing.T) {
	srv := staleProject(t)
	old := indexContent(t)
	srv.FailWith(500)
	code, _, stderr := run(t, "index", "--rebuild", "--yes")
	if code != 1 || !strings.HasPrefix(stderr, "ralph: ") || !strings.HasSuffix(stderr, "\nrun ralph index --rebuild --yes to continue\n") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if indexContent(t) != old {
		t.Error("the index changed")
	}
}

// slowProvider forwards to srv and makes each embedding request take a while.
func slowProvider(t *testing.T, srv *ollamatest.Server) {
	t.Helper()
	target, err := url.Parse(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			time.Sleep(300 * time.Millisecond)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Setenv("RALPH_EMBEDDINGS_URL", front.URL)
}

// interruptedRebuild starts a rebuild that is slow, interrupts it after its first record and returns what it printed.
func interruptedRebuild(t *testing.T, srv *ollamatest.Server) (code int, out string) {
	t.Helper()
	slowProvider(t, srv)
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- Run([]string{"index", "--rebuild", "--yes"}, &stdout, &stderr) }()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(stdout.String(), "[1/5] embedding ") {
		select {
		case <-deadline:
			t.Fatalf("no progress: %q %q", stdout.String(), stderr.String())
		case c := <-done:
			t.Fatalf("ended early with %d: %q %q", c, stdout.String(), stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("did not stop on SIGINT")
	}
	if stderr.String() != "" {
		t.Errorf("stderr %q", stderr.String())
	}
	return code, stdout.String()
}

func TestIndexCommandRebuildInterruptedExit130(t *testing.T) {
	srv := staleProject(t)
	old := indexContent(t)
	code, out := interruptedRebuild(t, srv)
	if code != 130 || !strings.HasSuffix(out, "interrupted; run ralph index --rebuild --yes to continue\n") {
		t.Fatalf("code %d\n%s", code, out)
	}
	if indexContent(t) != old {
		t.Error("the index changed")
	}
	if _, err := os.Stat(rebuildFile); err != nil {
		t.Errorf("rebuild file: %v", err)
	}

	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
	code, out, stderr := run(t, "index", "--rebuild", "--yes")
	if code != 0 || stderr != "" || !strings.Contains(out, "rebuilt 5 records") {
		t.Fatalf("second run: code %d stderr %q\n%s", code, stderr, out)
	}
	if n := strings.Count(out, "embedding "); n >= 5 || n == 0 {
		t.Errorf("second run embedded %d records, want fewer than 5", n)
	}
	if !strings.Contains(out, "] embedding ") || !strings.Contains(out, "/5] ") {
		t.Errorf("no running count:\n%s", out)
	}
}

func TestIndexCommandRebuildResumeMessage(t *testing.T) {
	srv := staleProject(t)
	if code, _ := interruptedRebuild(t, srv); code != 130 {
		t.Fatalf("code %d", code)
	}
	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
	code, out, stderr := run(t, "index", "--rebuild")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if !regexp.MustCompile(`(?m)^done already: [1-5] records, will continue\n`).MatchString(out) || strings.Contains(out, "starting again") {
		t.Errorf("output:\n%s", out)
	}
}

func TestIndexCommandRebuildStartingAgainMessage(t *testing.T) {
	srv := staleProject(t)
	if code, _ := interruptedRebuild(t, srv); code != 130 {
		t.Fatalf("code %d", code)
	}
	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
	t.Setenv("RALPH_EMBEDDINGS_CHUNK_CHARS", "900")
	code, out, _ := run(t, "index", "--rebuild")
	if code != 0 || !strings.Contains(out, "\nstarting again: the earlier rebuild used a different configuration\n") || strings.Contains(out, "done already") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestIndexCommandRebuildStatusConflict(t *testing.T) {
	staleProject(t)
	code, out, stderr := run(t, "index", "--rebuild", "--status")
	if code != 1 || out != "" || stderr != "ralph: --rebuild and --status cannot be used together\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIndexCommandRebuildNoProvider(t *testing.T) {
	seedSearchProject(t)
	code, out, stderr := run(t, "index", "--rebuild")
	if want := "ralph: no embedding provider is configured: set embeddings.provider to ollama\n"; code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIndexCommandRebuildJSON(t *testing.T) {
	srv := staleProject(t)
	p := rebuildPlanJSONOf(t)
	want := rebuildPlanJSON{Reasons: []string{"digest sha256:nomic -> sha256:repla"}, Records: 5, Chunks: 5, Model: "nomic-embed-text", URL: srv.URL()}
	if fmt.Sprint(p) != fmt.Sprint(want) {
		t.Errorf("plan = %+v, want %+v", p, want)
	}
	code, out, stderr := run(t, "index", "--rebuild", "--yes", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var r rebuildResultJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r != (rebuildResultJSON{Records: 5, Chunks: 5, Model: "nomic-embed-text", URL: srv.URL()}) {
		t.Errorf("result = %+v", r)
	}
	if p := rebuildPlanJSONOf(t); len(p.Reasons) != 0 || string(mustJSON(t, p.Reasons)) != "[]" {
		t.Errorf("not-stale plan = %+v", p)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIndexCommandStatusRebuildProgress(t *testing.T) {
	srv := staleProject(t)
	if code, out, _ := run(t, "index", "--status"); code != 0 || strings.Contains(out, "rebuild:") {
		t.Fatalf("before: code %d\n%s", code, out)
	}
	if code, _ := interruptedRebuild(t, srv); code != 130 {
		t.Fatalf("code %d", code)
	}
	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
	code, out, stderr := run(t, "index", "--status")
	if code != 0 || stderr != "" || !regexp.MustCompile(`(?m)^rebuild: [1-5] of 5 records$`).MatchString(out) {
		t.Fatalf("code %d stderr %q\n%s", code, stderr, out)
	}
}

func TestIndexCommandStatusRebuildJSON(t *testing.T) {
	srv := staleProject(t)
	code, out, _ := run(t, "index", "--status", "--json")
	if code != 0 || strings.Contains(out, `"rebuild"`) {
		t.Fatalf("before: code %d\n%s", code, out)
	}
	if code, _ := interruptedRebuild(t, srv); code != 130 {
		t.Fatalf("code %d", code)
	}
	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
	code, out, stderr := run(t, "index", "--status", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var st struct {
		Rebuild *rebuildStatus `json:"rebuild"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || st.Rebuild == nil || st.Rebuild.Total != 5 || st.Rebuild.Done < 1 || st.Rebuild.Done > 5 {
		t.Errorf("rebuild = %+v err %v\n%s", st.Rebuild, err, out)
	}
}
