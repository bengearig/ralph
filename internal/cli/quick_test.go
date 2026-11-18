package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewQuickServiceBuildsFromConfig(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/project.yaml", "pr:\n  base_branch: develop\ngit:\n  commit_trailer: false\nverify:\n  commands:\n    - make test\n    - make lint\n")
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	repo.Git("switch", "-c", "develop")
	repo.Git("add", "-A")
	repo.Git("commit", "-m", "project files")

	a := loadedApp(t)
	d := testutil.NewDB(t, db.Migrations)
	q, err := newQuickService(context.Background(), a, units.NewService(d), d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.Start(context.Background(), quick.StartInput{Title: "Try it"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "develop" {
		t.Errorf("base = %q, want develop", got.Base)
	}
	if want := []string{"make test", "make lint"}; len(got.Verify.Commands) != 2 || got.Verify.Commands[0] != want[0] || got.Verify.Commands[1] != want[1] {
		t.Errorf("verify commands = %v, want %v", got.Verify.Commands, want)
	}
	if cur := repo.Git("branch", "--show-current"); cur != got.Branch {
		t.Errorf("current branch = %q, want %q", cur, got.Branch)
	}
}

func TestNewQuickServiceOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	a := loadedApp(t)
	_, err := newQuickService(context.Background(), a, nil, nil)
	if err == nil || err.Error() != "not in a git repository" {
		t.Errorf("err = %v", err)
	}
}

func TestMCPServesQuickTools(t *testing.T) {
	inRepo(t)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	a := loadedApp(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, a, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range res.Tools {
		found[tool.Name] = true
	}
	for _, name := range []string{"quick_start", "quick_finish"} {
		if !found[name] {
			raw, _ := json.Marshal(found)
			t.Errorf("%s not listed: %s", name, raw)
		}
	}
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Errorf("runMCP: %v", err)
	}
}
