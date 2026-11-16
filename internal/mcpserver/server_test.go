package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/view"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(opts).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func staticOpen(svc units.Service) func(context.Context) (units.Service, error) {
	return func(context.Context) (units.Service, error) { return svc, nil }
}

func seeded(t *testing.T) units.Service {
	t.Helper()
	svc := units.NewService(testutil.NewDB(t, db.Migrations))
	ctx := units.WithActor(context.Background(), "Ann <ann@example.com>")
	mk := func(level units.Level, parent, title string) units.Unit {
		n := units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "b"}
		if level == units.LevelTask {
			n.AcceptanceCriteria = "c"
		}
		u, err := svc.Create(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	b := mk(units.LevelBrief, "", "brief")
	s := mk(units.LevelSlice, b.ID, "slice")
	mk(units.LevelTask, s.ID, "in slice")
	done := mk(units.LevelTask, "", "orphan done")
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		var err error
		if done, err = svc.SetState(ctx, done.ID, done.Fingerprint, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	mk(units.LevelTask, "", "orphan open")
	return svc
}

func callStatus(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "status", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func errorCode(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("IsError = false, content %v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var sc struct {
		Code string `json:"error_code"`
	}
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	return sc.Code
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestServerInfo(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Version: "1.2.3", Open: staticOpen(seeded(t))})
	info := cs.InitializeResult().ServerInfo
	if info.Name != "ralph" || info.Version != "1.2.3" {
		t.Errorf("server info = %+v", info)
	}
}

func TestToolsListHasStatus(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t))})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "status" {
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("annotations = %+v, want read-only", tool.Annotations)
		}
		for _, word := range []string{"orphan", "brief", "slice", "task"} {
			if !strings.Contains(tool.Description, word) {
				t.Errorf("description lacks %q", word)
			}
		}
		return
	}
	t.Fatal("no status tool listed")
}

func TestStatusToolMatchesView(t *testing.T) {
	svc := seeded(t)
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(svc)})
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%v", all), func(t *testing.T) {
			args := map[string]any{}
			if all {
				args["all"] = true
			}
			res := callStatus(t, cs, args)
			if res.IsError {
				t.Fatalf("error result: %s", text(res))
			}
			raw, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var got view.SummaryView
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			want, err := view.Summary(context.Background(), svc, all)
			if err != nil {
				t.Fatal(err)
			}
			want.Hidden = 0
			if !reflect.DeepEqual(got, want) {
				t.Errorf("structured = %+v, want %+v", got, want)
			}
			if text(res) == "" {
				t.Error("no text rendering")
			}
		})
	}
	if res := callStatus(t, cs, nil); strings.Contains(text(res), "orphan done") {
		t.Errorf("finished work shown without all: %s", text(res))
	}
}

func TestOpenIsLazyAndCached(t *testing.T) {
	var calls atomic.Int32
	svc := seeded(t)
	cs := connect(t, Options{Name: "ralph", Open: func(context.Context) (units.Service, error) {
		calls.Add(1)
		return svc, nil
	}})
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("Open called %d times before a tool call", n)
	}
	for range 3 {
		callStatus(t, cs, nil)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("Open called %d times, want 1", n)
	}
}

func TestOpenFailureNotCached(t *testing.T) {
	var calls atomic.Int32
	svc := seeded(t)
	cs := connect(t, Options{Name: "ralph", Open: func(context.Context) (units.Service, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("disk on fire")
		}
		return svc, nil
	}})
	if res := callStatus(t, cs, nil); errorCode(t, res) != "internal" {
		t.Errorf("first call: %v", res)
	}
	if res := callStatus(t, cs, nil); res.IsError {
		t.Errorf("second call failed: %s", text(res))
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("Open called %d times, want 2", n)
	}
}

func TestNotInitialized(t *testing.T) {
	for name, cause := range map[string]error{
		"db not found": fmt.Errorf("open: %w", db.ErrNotFound),
		"not exist":    fmt.Errorf("open: %w", os.ErrNotExist),
	} {
		t.Run(name, func(t *testing.T) {
			cs := connect(t, Options{Name: "ralph", Open: func(context.Context) (units.Service, error) { return nil, cause }})
			res := callStatus(t, cs, nil)
			if code := errorCode(t, res); code != "not_initialized" {
				t.Errorf("error_code = %q", code)
			}
			if !strings.Contains(text(res), "ralph init") {
				t.Errorf("text = %q, want it to mention ralph init", text(res))
			}
		})
	}
}

func TestInternalErrorCode(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: func(context.Context) (units.Service, error) { return nil, errors.New("boom") }})
	if code := errorCode(t, callStatus(t, cs, nil)); code != "internal" {
		t.Errorf("error_code = %q", code)
	}
}

func TestErrorResultsAreNotProtocolErrors(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: func(context.Context) (units.Service, error) { return nil, errors.New("boom") }})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false")
	}
}

type actorService struct {
	units.Service
	actor chan string
}

func (f *actorService) List(ctx context.Context, _ units.Filter) ([]units.Unit, error) {
	select {
	case f.actor <- units.ActorFrom(ctx):
	default:
	}
	return nil, nil
}

func (f *actorService) Statuses(context.Context, []string) (map[string]units.Status, error) {
	return map[string]units.Status{}, nil
}

func TestActorAttached(t *testing.T) {
	svc := &actorService{actor: make(chan string, 1)}
	cs := connect(t, Options{Name: "ralph", Actor: "Ann <ann@example.com>", Open: staticOpen(svc)})
	if res := callStatus(t, cs, nil); res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	if got := <-svc.actor; got != "Ann <ann@example.com>" {
		t.Errorf("actor = %q", got)
	}
}
