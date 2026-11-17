package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/testutil"
)

func useOllama(t *testing.T, srv *ollamatest.Server) {
	t.Helper()
	t.Setenv("RALPH_EMBEDDINGS_PROVIDER", "ollama")
	t.Setenv("RALPH_EMBEDDINGS_URL", srv.URL())
}

func TestIndexCommandNoProvider(t *testing.T) {
	seedSearchProject(t)
	code, out, stderr := run(t, "index")
	if want := "ralph: no embedding provider is configured: set embeddings.provider to ollama\n"; code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if code, _, stderr := run(t, "index", "--status"); code != 1 || !strings.Contains(stderr, "no embedding provider is configured") {
		t.Fatalf("status: code %d stderr %q", code, stderr)
	}
}

func TestIndexCommandEmbedsAndSummarizes(t *testing.T) {
	f := seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	code, out, stderr := run(t, "index")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 || lines[5] != "indexed 5 records (5 chunks), removed 0" {
		t.Fatalf("output %q", out)
	}
	for _, l := range lines[:5] {
		if !strings.HasPrefix(l, "embedding ") || !strings.HasSuffix(l, " (1 chunks)") {
			t.Errorf("line %q", l)
		}
	}
	if lines[2] != "embedding "+f.task+" (1 chunks)" || lines[3] != "embedding "+f.doc+" (1 chunks)" || !strings.HasPrefix(lines[4], "embedding note N-1 on "+f.task) {
		t.Errorf("order %q", lines)
	}
	if _, err := os.Stat(filepath.Join(".ralph", "index.db")); err != nil {
		t.Fatal(err)
	}
	ignore, _ := os.ReadFile(filepath.Join(".ralph", ".gitignore"))
	if !strings.Contains(string(ignore), "/index.db*") {
		t.Errorf("gitignore = %q", ignore)
	}
}

func TestIndexCommandUpToDate(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	embeds := func() (n int) {
		for _, r := range srv.Requests() {
			if r.Path == "/api/embed" {
				n++
			}
		}
		return n
	}
	before := embeds()
	code, out, stderr := run(t, "index")
	if want := "indexed 0 records (0 chunks), removed 0\nindex is up to date\n"; code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if embeds() != before {
		t.Error("an up to date index embedded something")
	}
}

func TestIndexCommandUnreachableProvider(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	srv.Close()
	code, _, stderr := run(t, "index")
	if code != 1 || !strings.HasPrefix(stderr, "ralph: embedding ") || !strings.Contains(stderr, "unavailable") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestIndexCommandMissingModel(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{Models: []string{"other:latest"}})
	useOllama(t, srv)
	code, _, stderr := run(t, "index")
	if code != 1 || !strings.Contains(stderr, "ollama pull nomic-embed-text") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestIndexCommandStatus(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	wd, _ := os.Getwd()
	root, _ := filepath.EvalSymlinks(wd)
	file := regexp.MustCompile(`^file: ` + regexp.QuoteMeta(filepath.Join(root, ".ralph", "index.db")) + ` \(\d+\.\d KiB\)$`)

	check := func(out string, want ...string) {
		t.Helper()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) < len(want)+1 || !file.MatchString(lines[len(want)]) {
			t.Fatalf("output %q", out)
		}
		for i, w := range want {
			if lines[i] != w {
				t.Errorf("line %d = %q, want %q", i, lines[i], w)
			}
		}
	}
	code, out, stderr := run(t, "index", "--status")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	check(out, "provider: ollama "+srv.URL(), "model: nomic-embed-text sha256:nomic", "reachable: yes", "dimensions: none yet",
		"records: 0 of 5 indexed, 5 waiting, 0 to remove")
	if n := len(srv.Requests()); n == 0 {
		t.Fatal("status never asked the provider")
	}
	for _, r := range srv.Requests() {
		if r.Path == "/api/embed" {
			t.Fatal("status embedded something")
		}
	}

	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	_, out, _ = run(t, "index", "--status")
	check(out, "provider: ollama "+srv.URL(), "model: nomic-embed-text sha256:nomic", "reachable: yes", "dimensions: 64",
		"records: 5 of 5 indexed, 0 waiting, 0 to remove")

	srv.Close()
	code, out, _ = run(t, "index", "--status")
	if code != 0 {
		t.Fatalf("unreachable status exits %d", code)
	}
	lines := strings.Split(out, "\n")
	if lines[1] != "model: nomic-embed-text" || !strings.HasPrefix(lines[2], "reachable: no (") || lines[3] != "dimensions: 64" {
		t.Fatalf("output %q", out)
	}
}

func TestIndexCommandStatusJSON(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	code, out, stderr := run(t, "index", "--status", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if got["provider"] != "ollama" || got["url"] != srv.URL() || got["model"] != "nomic-embed-text" || got["digest"] != "sha256:nomic" ||
		got["reachable"] != true || got["dimensions"] != float64(0) || got["indexed"] != float64(0) || got["total"] != float64(5) ||
		got["waiting"] != float64(5) || got["gone"] != float64(0) || !strings.HasSuffix(got["file"].(string), "index.db") {
		t.Fatalf("object %v", got)
	}
	if _, ok := got["reason"]; ok {
		t.Error("reason present while reachable")
	}
	if code, _, stderr := run(t, "index", "--json"); code != 1 || !strings.Contains(stderr, "--status") {
		t.Fatalf("--json alone: code %d stderr %q", code, stderr)
	}
}

func TestIndexCommandNotInitialized(t *testing.T) {
	inRepo(t)
	code, out, stderr := run(t, "index")
	if want := "ralph: " + errNoDatabase + "\n"; code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if !strings.Contains(stderr, `ralph init`) {
		t.Error("message does not say ralph init")
	}
}

func TestIndexCommandOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "index")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestIndexCommandHelpListsFlags(t *testing.T) {
	code, out, _ := run(t, "index", "--help")
	if code != 0 || !strings.Contains(out, "--status") || !strings.Contains(out, "--json") {
		t.Fatalf("code %d out %q", code, out)
	}
	if _, root, _ := run(t, "--help"); !strings.Contains(root, "index") {
		t.Error("root help does not list index")
	}
}

func TestIndexCommandBusy(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	release, err := semantic.LockIndex(".ralph")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	before := len(srv.Requests())
	code, out, stderr := run(t, "index")
	if want := "ralph: another ralph index run is in progress\n"; code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	for _, r := range srv.Requests()[before:] {
		if strings.Contains(r.Path, "embed") {
			t.Fatalf("embedded: %+v", r)
		}
	}
}

func TestIndexCommandStatusIgnoresLock(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	release, err := semantic.LockIndex(".ralph")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if code, out, stderr := run(t, "index", "--status"); code != 0 || !strings.Contains(out, "state:") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}
