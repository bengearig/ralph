package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type issueFixture struct {
	*statusEnv
	srv *jiratest.Server
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	e := newStatusEnv(t)
	srv := jiratest.NewServer(t, jiratest.Options{})
	t.Setenv("RALPH_ISSUES_PROVIDER", "jira")
	t.Setenv("RALPH_JIRA_SITE", srv.URL())
	t.Setenv("RALPH_JIRA_USER", srv.User())
	t.Setenv("JIRA_TOKEN", srv.Token())
	return &issueFixture{statusEnv: e, srv: srv}
}

func TestOpenIssueSourceNoProvider(t *testing.T) {
	newStatusEnv(t)
	_, err := openIssueSource(context.Background(), loadedApp(t))
	if err == nil || err.Error() != "no issue source is configured: set issues.provider to jira" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenIssueSourceMissingSettingNamed(t *testing.T) {
	for _, tc := range []struct{ missing, site, user string }{
		{"jira.site", "", "me@example.com"},
		{"jira.user", "https://example.atlassian.net", ""},
	} {
		t.Run(tc.missing, func(t *testing.T) {
			newStatusEnv(t)
			t.Setenv("RALPH_ISSUES_PROVIDER", "jira")
			t.Setenv("RALPH_JIRA_SITE", tc.site)
			t.Setenv("RALPH_JIRA_USER", tc.user)
			_, err := openIssueSource(context.Background(), loadedApp(t))
			if err == nil || !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("err = %v, want it to name %s", err, tc.missing)
			}
		})
	}
}

func TestOpenIssueSourceTokenFromEnvironment(t *testing.T) {
	f := newIssueFixture(t)
	key := f.srv.Seed(jiratest.Issue{Summary: "Hello"})
	src, err := openIssueSource(context.Background(), loadedApp(t))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := src.Parse(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Get(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(f.srv.User()+":"+f.srv.Token()))
	for _, r := range f.srv.Requests() {
		if r.Header.Get("Authorization") == want {
			return
		}
	}
	t.Errorf("no request carried %q", want)
}

func TestIssueShowTextExact(t *testing.T) {
	f := newIssueFixture(t)
	full := f.srv.Seed(jiratest.Issue{Type: "Story", Summary: "Add login", Status: "To Do", Body: "Users sign in.", Parent: ""})
	parent := f.srv.Seed(jiratest.Issue{Type: "Epic", Summary: "Accounts", Status: "To Do"})
	child := f.srv.Seed(jiratest.Issue{Type: "Task", Summary: "Child work", Status: "In Progress", Parent: parent})

	code, out, stderr := run(t, "issue", "show", full)
	want := full + "  Story  To Do  Add login\n" + f.srv.URL() + "/browse/" + full + "\n\nUsers sign in.\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d\nout %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
	code, out, _ = run(t, "issue", "show", child)
	want = child + "  Task  In Progress  Child work\n" + f.srv.URL() + "/browse/" + child + "\nparent: " + parent + "\n"
	if code != 0 || out != want {
		t.Fatalf("code %d\nout %q\nwant %q", code, out, want)
	}
}

func TestIssueShowJSON(t *testing.T) {
	f := newIssueFixture(t)
	key := f.srv.Seed(jiratest.Issue{Type: "Story", Summary: "Add login", Status: "To Do", Body: "Users sign in."})
	code, out, stderr := run(t, "issue", "show", key, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(got) != 9 || got["key"] != key || got["system"] != "jira" || got["title"] != "Add login" ||
		got["type"] != "Story" || got["status"] != "To Do" || got["parent"] != "" || got["body"] != "Users sign in." ||
		got["url"] != f.srv.URL()+"/browse/"+key || !strings.HasPrefix(got["instruction"], "Jira "+key+": Add login") {
		t.Fatalf("json = %q", out)
	}
}

func TestIssueShowByURL(t *testing.T) {
	f := newIssueFixture(t)
	key := f.srv.Seed(jiratest.Issue{Summary: "By address"})
	code, out, stderr := run(t, "issue", "show", f.srv.URL()+"/browse/"+key)
	if code != 0 || !strings.HasPrefix(out, key+"  ") || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIssueShowNotFoundExit1(t *testing.T) {
	f := newIssueFixture(t)
	code, out, stderr := run(t, "issue", "show", f.srv.Project()+"-999")
	if code != 1 || out != "" || !strings.Contains(stderr, f.srv.Project()+"-999") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIssueShowInvalidRefExit1(t *testing.T) {
	newIssueFixture(t)
	code, out, stderr := run(t, "issue", "show", "nonsense")
	if code != 1 || out != "" || !strings.Contains(stderr, "not a Jira ticket") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIssueShowNotInitialized(t *testing.T) {
	inRepo(t)
	code, out, stderr := run(t, "issue", "show", "ABC-1")
	if code != 1 || out != "" || !strings.Contains(stderr, "ralph init") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIssueShowOutsideRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, stderr := run(t, "issue", "show", "ABC-1")
	if code != 1 || out != "" || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestIssueShowHelp(t *testing.T) {
	for _, args := range [][]string{{"issue", "--help"}, {"issue", "show", "--help"}} {
		code, out, stderr := run(t, args...)
		if code != 0 || stderr != "" || !strings.Contains(out, "show") {
			t.Fatalf("%v: code %d out %q stderr %q", args, code, out, stderr)
		}
	}
	_, out, _ := run(t, "issue", "show", "--help")
	for _, want := range []string{"ralph issue show <ref>", "--json"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q: %s", want, out)
		}
	}
}

func TestMCPServerListsIssueGetWhenConfigured(t *testing.T) {
	f := newIssueFixture(t)
	key := f.srv.Seed(jiratest.Issue{Type: "Story", Summary: "Over MCP", Status: "To Do"})
	cs := mcpSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "issue_get" {
			found = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		}
	}
	if !found {
		t.Fatal("issue_get not listed as read-only")
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "issue_get", Arguments: map[string]any{"ref": key}})
	if err != nil || res.IsError {
		t.Fatalf("err %v res %+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"title":"Over MCP"`) {
		t.Errorf("structured = %s", raw)
	}
}
