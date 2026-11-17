package all

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/harnesstest"
)

func TestAdaptersRegistry(t *testing.T) {
	names := Names()
	if !sort.StringsAreSorted(names) {
		t.Errorf("Names() not sorted: %v", names)
	}
	if len(names) != len(Adapters()) {
		t.Errorf("Names() has %d entries for %d adapters", len(names), len(Adapters()))
	}
	for _, n := range names {
		a, ok := ByName(n)
		if !ok || a.Name() != n {
			t.Errorf("ByName(%q) = %v, %v", n, a, ok)
		}
	}
	if a, ok := ByName("no-such-harness"); ok || a != nil {
		t.Errorf("ByName(unknown) = %v, %v; want not found", a, ok)
	}
}

func TestCheckAllOnRegisteredAdapters(t *testing.T) {
	harnesstest.CheckAll(t, Adapters())
}

func fixturesOf(t *testing.T, dir string) harnesstest.HeadlessFixtures {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", dir, "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return harnesstest.HeadlessFixtures{Success: read("success.jsonl"), Error: read("error.jsonl"), Truncated: read("truncated.jsonl")}
}

func TestHeadlessConformance(t *testing.T) {
	dirs := map[string]string{"claude-code": "claudecode", "opencode": "opencode"}
	for _, a := range Adapters() {
		dir, ok := dirs[a.Name()]
		if !ok {
			t.Fatalf("no fixtures for adapter %q", a.Name())
		}
		t.Run(a.Name(), func(t *testing.T) {
			harnesstest.RunHeadless(t, a, fixturesOf(t, dir))
		})
	}
}

func TestAllAdaptersImplementHeadless(t *testing.T) {
	req := harness.HeadlessRequest{
		Dir:    "/work/project",
		Prompt: "go",
		MCP:    harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
	}
	for _, a := range Adapters() {
		inv, err := a.Headless(req)
		if err != nil {
			t.Errorf("%s: %v", a.Name(), err)
			continue
		}
		if inv.Command == "" || inv.Dir != req.Dir || inv.Stdin != req.Prompt {
			t.Errorf("%s: invocation %+v", a.Name(), inv)
		}
		if a.NewParser() == nil {
			t.Errorf("%s: NewParser returned nil", a.Name())
		}
	}
}
