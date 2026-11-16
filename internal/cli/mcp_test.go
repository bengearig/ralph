package cli

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/view"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func TestMCPCommandServesStatus(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(svc units.Service) {
		e.create(svc, units.LevelBrief, "", "the brief", "")
	})
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
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("error result: %v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var got view.SummaryView
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || got.Briefs[0].ID != "B-1" || got.Briefs[0].Title != "the brief" {
		t.Errorf("summary = %+v", got)
	}
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Errorf("runMCP: %v", err)
	}
}

func TestMCPEndsOnEndOfInput(t *testing.T) {
	inRepo(t)
	a := loadedApp(t)

	pr, pw := io.Pipe()
	var out nopWriteCloser
	done := make(chan error, 1)
	go func() { done <- runMCP(context.Background(), a, &mcp.IOTransport{Reader: pr, Writer: out}) }()
	_ = pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runMCP: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runMCP did not return on end of input")
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func loadedApp(t *testing.T) *app {
	t.Helper()
	a := &app{}
	if err := a.load(&cobra.Command{}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestMCPOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "mcp")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if want := "ralph: not in a git repository\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func planningConfirm(t *testing.T) bool {
	t.Helper()
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
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "planning_settings"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("error result: %v", res.Content)
	}
	m, _ := res.StructuredContent.(map[string]any)
	got, _ := m["confirm_outline"].(bool)
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Errorf("runMCP: %v", err)
	}
	return got
}

func TestMCPPlanningFromProjectFile(t *testing.T) {
	repo := newStatusEnv(t).repo
	if planningConfirm(t) {
		t.Error("confirm_outline true without the setting")
	}
	repo.WriteFile(".ralph/project.yaml", "plan:\n  confirm_outline: true\n")
	if !planningConfirm(t) {
		t.Error("confirm_outline false with the setting in the project file")
	}
}

func TestMCPPlanningFromEnvironment(t *testing.T) {
	repo := newStatusEnv(t).repo
	repo.WriteFile(".ralph/project.yaml", "plan:\n  confirm_outline: false\n")
	t.Setenv("RALPH_PLAN_CONFIRM_OUTLINE", "true")
	if !planningConfirm(t) {
		t.Error("environment did not win over the project file")
	}
}
