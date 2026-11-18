package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/issuesource/issuetest"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newIssueClient(t *testing.T, src issuesource.Source) *mcp.ClientSession {
	t.Helper()
	return connect(t, Options{
		Name: "ralph", Actor: actor,
		Issues: func(context.Context) (issuesource.Source, error) { return src, nil },
	})
}

func seededFake() *issuetest.Fake {
	f := issuetest.NewFake("jira", "example.atlassian.net")
	f.Add(issuesource.Issue{
		Ref: issuesource.Ref{Key: "ABC-1"}, Title: "Fix the thing", Body: "It is broken.",
		Type: "Bug", Status: "To Do", Parent: "ABC-9",
	})
	return f
}

func callIssueGet(t *testing.T, cs *mcp.ClientSession, ref string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "issue_get", Arguments: map[string]any{"ref": ref}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestIssueGetReturnsFields(t *testing.T) {
	cs := newIssueClient(t, seededFake())
	res := callIssueGet(t, cs, "abc-1")
	if res.IsError {
		t.Fatalf("error: %s", text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	delete(all, "notices")
	got := map[string]string{}
	for k, v := range all {
		got[k], _ = v.(string)
	}
	want := issuesource.Instruction(issuesource.Issue{
		Ref: issuesource.Ref{System: "jira", Key: "ABC-1"}, Title: "Fix the thing", Body: "It is broken.",
	})
	expect := map[string]string{
		"key": "ABC-1", "url": "https://example.atlassian.net/browse/ABC-1", "title": "Fix the thing", "type": "Bug",
		"status": "To Do", "parent": "ABC-9", "body": "It is broken.", "instruction": want, "system": "jira",
	}
	for k, v := range expect {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(expect) {
		t.Errorf("fields = %v", got)
	}
}

func TestIssueGetReadOnlyAnnotation(t *testing.T) {
	tool, ok := tools(t, newIssueClient(t, seededFake()))["issue_get"]
	if !ok {
		t.Fatal("issue_get not listed")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("annotations = %+v", tool.Annotations)
	}
	if !strings.Contains(tool.Description, "writes nothing") {
		t.Errorf("description = %q", tool.Description)
	}
}

func TestIssueGetWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	d, err := db.Open(context.Background(), path, db.Options{Migrations: db.Migrations})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(units.NewService(d)),
		Issues: func(context.Context) (issuesource.Source, error) { return seededFake(), nil },
	})
	if res := callIssueGet(t, cs, "ABC-1"); res.IsError {
		t.Fatalf("error: %s", text(res))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the database file changed")
	}
}

func TestIssueGetInvalidRefMessage(t *testing.T) {
	f := seededFake()
	_, parseErr := f.Parse("nonsense")
	res := callIssueGet(t, newIssueClient(t, f), "nonsense")
	if code := errorCode(t, res); code != "invalid" {
		t.Errorf("code = %q", code)
	}
	if !strings.Contains(text(res), parseErr.Error()) {
		t.Errorf("text = %q, want it to carry %q", text(res), parseErr)
	}
}

func TestIssueGetNotFound(t *testing.T) {
	res := callIssueGet(t, newIssueClient(t, seededFake()), "ABC-404")
	if code := errorCode(t, res); code != "not_found" {
		t.Errorf("code = %q", code)
	}
	if !strings.Contains(text(res), "ABC-404") {
		t.Errorf("text = %q", text(res))
	}
}

func TestIssueGetNotConfigured(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Actor: actor})
	if _, ok := tools(t, cs)["issue_get"]; ok {
		t.Error("issue_get is listed without Options.Issues")
	}
}

func failingGet(t *testing.T, err error, wantCode, wantText string) {
	t.Helper()
	f := seededFake()
	f.FailWith(err)
	res := callIssueGet(t, newIssueClient(t, f), "ABC-1")
	if code := errorCode(t, res); code != wantCode {
		t.Errorf("code = %q, want %q", code, wantCode)
	}
	if !strings.Contains(text(res), wantText) {
		t.Errorf("text = %q, want it to contain %q", text(res), wantText)
	}
}

func TestIssueGetUnauthorized(t *testing.T) {
	failingGet(t, taskmanager.NewCredentialError("jira.token", "the site refused the token"), "unauthorized", "jira.token")
}

func TestIssueGetRateLimited(t *testing.T) {
	failingGet(t, &taskmanager.RateLimitError{}, "rate_limited", "rate limited")
}

func TestIssueGetUnavailable(t *testing.T) {
	failingGet(t, issuesource.ErrUnavailable, "unavailable", "unavailable")
}

func TestIssueGetFactoryErrorReported(t *testing.T) {
	cs := connect(t, Options{
		Name: "ralph", Actor: actor,
		Issues: func(context.Context) (issuesource.Source, error) { return nil, errors.New("jira.site is not set") },
	})
	res := callIssueGet(t, cs, "ABC-1")
	if !res.IsError || !strings.Contains(text(res), "jira.site is not set") {
		t.Errorf("result = %v", text(res))
	}
	cs = connect(t, Options{
		Name: "ralph", Actor: actor,
		Issues: func(context.Context) (issuesource.Source, error) {
			return nil, &NotConfiguredError{Message: "no issue source is configured: set issues.provider to jira"}
		},
	})
	res = callIssueGet(t, cs, "ABC-1")
	if code := errorCode(t, res); code != "not_configured" || !strings.Contains(text(res), "set issues.provider to jira") {
		t.Errorf("code %q text %q", code, text(res))
	}
}

func TestIssueGetFactoryCalledOnce(t *testing.T) {
	var calls atomic.Int32
	f := seededFake()
	cs := connect(t, Options{
		Name: "ralph", Actor: actor,
		Issues: func(context.Context) (issuesource.Source, error) { calls.Add(1); return f, nil },
	})
	if calls.Load() != 0 {
		t.Fatal("factory called before first use")
	}
	for range 3 {
		callIssueGet(t, cs, "ABC-1")
	}
	if calls.Load() != 1 {
		t.Errorf("factory called %d times", calls.Load())
	}
}
