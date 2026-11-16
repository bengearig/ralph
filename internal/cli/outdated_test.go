package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const statusCommandPath = ".claude/commands/ralph/status.md"

// makeOutdated makes the installed status command look like an older ralph
// wrote it: unchanged since it was recorded, but different from what this
// ralph would write.
func makeOutdated(t *testing.T, root string) {
	t.Helper()
	old := []byte("an older version\n")
	if err := os.WriteFile(filepath.Join(root, statusCommandPath), old, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, project.DirName, project.IntegrationFile)
	m, err := integration.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(old)
	found := false
	for i := range m.Entries {
		if m.Entries[i].Path == statusCommandPath {
			m.Entries[i].SHA256 = hex.EncodeToString(sum[:])
			found = true
		}
	}
	if !found {
		t.Fatal("status command not recorded")
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
}

func outdatedEnv(t *testing.T) *statusEnv {
	t.Helper()
	withFakeClaude(t)
	return newStatusEnv(t)
}

func TestStatusOutdatedLine(t *testing.T) {
	e := outdatedEnv(t)
	_, base, _ := e.status()
	if strings.Contains(base, "out of date") {
		t.Fatalf("line present when up to date:\n%s", base)
	}
	makeOutdated(t, e.repo.Dir)
	code, out, stderr := e.status()
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	want := base + "\n" + integrationOutdatedLine + "\n"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestStatusOutdatedJSON(t *testing.T) {
	e := outdatedEnv(t)
	decode := func() map[string]any {
		t.Helper()
		code, out, stderr := e.status("--json")
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := decode(); v["integration_outdated"] != nil || v["briefs"] == nil {
		t.Errorf("up to date: %v", v)
	}
	makeOutdated(t, e.repo.Dir)
	v := decode()
	list, _ := v["integration_outdated"].([]any)
	if len(list) != 1 || list[0] != statusCommandPath || v["briefs"] == nil {
		t.Errorf("outdated: %v", v)
	}
}

func TestStatusOutdatedCheckErrorDoesNotFail(t *testing.T) {
	e := outdatedEnv(t)
	bad := filepath.Join(e.repo.Dir, project.DirName, project.IntegrationFile)
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := e.status()
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(out, "out of date") || !strings.Contains(stderr, project.IntegrationFile) {
		t.Errorf("out = %q, stderr = %q", out, stderr)
	}
}

func TestStatusNoManifestAddsNothing(t *testing.T) {
	withoutClaude(t)
	e := newStatusEnv(t)
	if _, out, stderr := e.status(); strings.Contains(out, "out of date") || stderr != "" {
		t.Errorf("out = %q, stderr = %q", out, stderr)
	}
}

func notices(t *testing.T, cs *mcp.ClientSession, ctx context.Context) []string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := res.StructuredContent.(map[string]any)
	var codes []string
	list, _ := m["notices"].([]any)
	for _, n := range list {
		nm, _ := n.(map[string]any)
		if nm["message"] != `ralph's integration files are out of date. Run "ralph init" to update them.` {
			t.Errorf("message = %v", nm["message"])
		}
		codes = append(codes, nm["code"].(string))
	}
	return codes
}

func connectMCP(t *testing.T, e *statusEnv) (*mcp.ClientSession, context.Context) {
	t.Helper()
	a := loadedApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = runMCP(ctx, a, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func TestMCPOutdatedNotice(t *testing.T) {
	e := outdatedEnv(t)
	cs, ctx := connectMCP(t, e)
	if got := notices(t, cs, ctx); len(got) != 0 {
		t.Fatalf("up to date: %v", got)
	}
	makeOutdated(t, e.repo.Dir)
	if got := notices(t, cs, ctx); len(got) != 1 || got[0] != "integration_outdated" {
		t.Fatalf("outdated: %v", got)
	}
	if got := notices(t, cs, ctx); len(got) != 0 {
		t.Fatalf("repeated: %v", got)
	}
}

func TestInitClearsOutdated(t *testing.T) {
	e := outdatedEnv(t)
	makeOutdated(t, e.repo.Dir)
	cs, ctx := connectMCP(t, e)
	if _, out, _ := e.status(); !strings.Contains(out, integrationOutdatedLine) {
		t.Fatalf("no line before init:\n%s", out)
	}
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	if _, out, _ := e.status(); strings.Contains(out, "out of date") {
		t.Errorf("line after init:\n%s", out)
	}
	if got := notices(t, cs, ctx); len(got) != 0 {
		t.Errorf("notice after init: %v", got)
	}
}

func TestStatusUnfinalizedBeforeIntegrationLine(t *testing.T) {
	e := outdatedEnv(t)
	seedUnfinalized(e)
	makeOutdated(t, e.repo.Dir)
	_, out, _ := e.status()
	hint := strings.Index(out, finishHint)
	if hint < 0 || !strings.HasSuffix(out, finishHint+"\n\n"+integrationOutdatedLine+"\n") {
		t.Errorf("out = %q", out)
	}
}

func TestMCPUnfinalizedNotice(t *testing.T) {
	e := outdatedEnv(t)
	ids := seedUnfinalized(e)
	cs, ctx := connectMCP(t, e)
	call := func() (codes, texts []string) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
		if err != nil {
			t.Fatal(err)
		}
		m, _ := res.StructuredContent.(map[string]any)
		list, _ := m["notices"].([]any)
		for _, n := range list {
			nm, _ := n.(map[string]any)
			codes = append(codes, nm["code"].(string))
			texts = append(texts, nm["message"].(string))
		}
		return codes, texts
	}
	codes, texts := call()
	if len(codes) != 2 || codes[0] != "quick_unfinalized" || codes[1] != "quick_unfinalized" ||
		!strings.HasPrefix(texts[0], ids[0]+` "First" was started and not finalized (branch ralph/t-1-first).`) {
		t.Fatalf("notices = %v %v", codes, texts)
	}
	if codes, _ := call(); len(codes) != 0 {
		t.Fatalf("repeated: %v", codes)
	}
	e.with(func(s units.Service) {
		for _, id := range ids {
			u, err := s.Get(e.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetState(e.ctx, id, u.Fingerprint, units.StateDone, ""); err != nil {
				t.Fatal(err)
			}
		}
	})
	if codes, _ := call(); len(codes) != 0 {
		t.Fatalf("after done: %v", codes)
	}
}
