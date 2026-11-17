package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/vecindex"
)

func embedRequests(srv *ollamatest.Server) (n int) {
	for _, r := range srv.Requests() {
		if r.Path == "/api/embed" {
			n++
		}
	}
	return n
}

func indexedProject(t *testing.T) *ollamatest.Server {
	t.Helper()
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	return srv
}

func deleteRecordedConfig(t *testing.T) {
	t.Helper()
	wd, _ := os.Getwd()
	idx, err := vecindex.Open(filepath.Join(wd, ".ralph", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if err := idx.DeleteMeta(context.Background(), "config"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexCommandStaleRefuses(t *testing.T) {
	srv := indexedProject(t)
	srv.AddModel("nomic-embed-text", "sha256:replaced")
	if _, err := os.Stat(filepath.Join(".ralph", "index.db")); err != nil {
		t.Fatal(err)
	}
	embeds := embedRequests(srv)
	code, out, stderr := run(t, "index")
	want := "ralph: the semantic index was built with a different embedding configuration (digest sha256:nomic -> sha256:repla); run \"ralph index --rebuild\" to rebuild it\n"
	if code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if embedRequests(srv) != embeds {
		t.Error("a stale index was extended")
	}
}

func TestIndexCommandStaleLegacyReason(t *testing.T) {
	srv := indexedProject(t)
	deleteRecordedConfig(t)
	embeds := embedRequests(srv)
	code, _, stderr := run(t, "index")
	if code != 1 || !strings.Contains(stderr, "(built before its configuration was recorded); run \"ralph index --rebuild\" to rebuild it") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if embedRequests(srv) != embeds {
		t.Error("a stale index was extended")
	}
}

func TestIndexCommandStatusBuiltWithAndStateOK(t *testing.T) {
	indexedProject(t)
	code, out, stderr := run(t, "index", "--status")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	n := len(lines)
	if lines[n-2] != "built with: nomic-embed-text sha256:nomic 64 dimensions, chunk size 1200" || lines[n-1] != "state: ok" {
		t.Errorf("output %q", out)
	}
}

func TestIndexCommandStatusStateStale(t *testing.T) {
	srv := indexedProject(t)
	srv.AddModel("nomic-embed-text", "sha256:replaced")
	if code, _, stderr := run(t, "index"); code != 1 {
		t.Fatal(stderr)
	}
	code, out, _ := run(t, "index", "--status")
	if code != 0 || !strings.HasSuffix(out, "\nstate: stale (digest sha256:nomic -> sha256:repla)\n") {
		t.Fatalf("code %d output %q", code, out)
	}
}

func TestIndexCommandStatusStateEmpty(t *testing.T) {
	seedSearchProject(t)
	useOllama(t, ollamatest.NewServer(t, ollamatest.Options{}))
	code, out, _ := run(t, "index", "--status")
	if code != 0 || !strings.HasSuffix(out, "\nbuilt with: nothing indexed yet\nstate: empty\n") {
		t.Fatalf("code %d output %q", code, out)
	}
}

func TestIndexCommandStatusJSONState(t *testing.T) {
	srv := indexedProject(t)
	_, out, _ := run(t, "index", "--status", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	bw, _ := got["built_with"].(map[string]any)
	if got["state"] != "ok" || bw["model"] != "nomic-embed-text" || bw["dimensions"] != float64(64) {
		t.Errorf("object %v", got)
	}
	if _, ok := got["stale_reasons"]; ok {
		t.Error("stale_reasons present while ok")
	}

	srv.AddModel("nomic-embed-text", "sha256:replaced")
	if code, _, stderr := run(t, "index"); code != 1 {
		t.Fatal(stderr)
	}
	_, out, _ = run(t, "index", "--status", "--json")
	got = nil
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	reasons, _ := got["stale_reasons"].([]any)
	if got["state"] != "stale" || len(reasons) != 1 || reasons[0] != "digest sha256:nomic -> sha256:repla" {
		t.Errorf("object %v", got)
	}
}

func TestIndexCommandStatusJSONEmpty(t *testing.T) {
	seedSearchProject(t)
	useOllama(t, ollamatest.NewServer(t, ollamatest.Options{}))
	_, out, _ := run(t, "index", "--status", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["built_with"]; got["state"] != "empty" || ok {
		t.Errorf("object %v", got)
	}
}

func TestIndexCommandStatusBuiltWithNotRecorded(t *testing.T) {
	indexedProject(t)
	deleteRecordedConfig(t)
	code, out, _ := run(t, "index", "--status")
	if code != 0 || !strings.HasSuffix(out, "\nbuilt with: not recorded\nstate: stale (built before its configuration was recorded)\n") {
		t.Fatalf("code %d output %q", code, out)
	}
}
