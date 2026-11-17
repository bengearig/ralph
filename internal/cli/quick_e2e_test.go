package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const quickProjectConfig = `github:
  owner: acme
  repo: widgets
verify:
  commands:
    - test -f feature.txt
    - echo verified
`

type fakeGitHub struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []string
	created  []map[string]any
	pulls    []*fakePull
	feedback map[int][]fakeFeedback
	nextID   int

	// headSHA reports the tip of a branch on the origin; the head commit of an
	// open pull request follows it and a merged or closed one keeps the tip it
	// had when it ended.
	headSHA func(branch string) string
}

type fakePull struct {
	closed, merged bool
	sha            string
}

type fakeFeedback struct {
	id         int
	kind       string
	user, body string
	path       string
	line       int
	state      string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{feedback: map[int][]fakeFeedback{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.requests = append(g.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			g.created = append(g.created, in)
			g.pulls = append(g.pulls, &fakePull{})
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(g.pull(len(g.created)))
		case http.MethodGet:
			out := []map[string]any{}
			head := strings.TrimPrefix(r.URL.Query().Get("head"), "acme:")
			for i := len(g.created); i >= 1; i-- {
				if head == "" || g.created[i-1]["head"] == head {
					out = append(out, g.pull(i))
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/", func(w http.ResponseWriter, r *http.Request) {
		g.serveNumbered(w, r, strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/pulls/"), "")
	})
	mux.HandleFunc("/repos/acme/widgets/issues/", func(w http.ResponseWriter, r *http.Request) {
		g.serveNumbered(w, r, strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/issues/"), "issues")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.requests = append(g.requests, r.Method+" "+r.URL.Path)
		g.mu.Unlock()
		http.NotFound(w, r)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// serveNumbered answers GET /pulls/{n}, /pulls/{n}/comments, /pulls/{n}/reviews
// and /issues/{n}/comments.
func (g *fakeGitHub) serveNumbered(w http.ResponseWriter, r *http.Request, rest, area string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		return
	}
	numStr, sub, _ := strings.Cut(rest, "/")
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 1 || n > len(g.pulls) {
		http.NotFound(w, r)
		return
	}
	switch {
	case area == "" && sub == "":
		_ = json.NewEncoder(w).Encode(g.pull(n))
	case area == "" && sub == "comments":
		_ = json.NewEncoder(w).Encode(g.feedbackJSON(n, "inline"))
	case area == "" && sub == "reviews":
		_ = json.NewEncoder(w).Encode(g.feedbackJSON(n, "review"))
	case area == "issues" && sub == "comments":
		_ = json.NewEncoder(w).Encode(g.feedbackJSON(n, "comment"))
	default:
		http.NotFound(w, r)
	}
}

func (g *fakeGitHub) pull(n int) map[string]any {
	in, st := g.created[n-1], g.pulls[n-1]
	state := "open"
	if st.closed || st.merged {
		state = "closed"
	}
	out := map[string]any{
		"number":   n,
		"html_url": g.srv.URL + "/acme/widgets/pull/" + strconv.Itoa(n),
		"title":    in["title"],
		"body":     in["body"],
		"state":    state,
		"draft":    in["draft"],
		"head":     map[string]any{"ref": in["head"], "sha": g.headOf(n)},
		"base":     map[string]any{"ref": in["base"]},
	}
	if st.merged {
		out["merged_at"] = "2026-01-02T00:00:00Z"
		out["merge_commit_sha"] = "0123456789abcdef0123456789abcdef01234567"
	}
	return out
}

func (g *fakeGitHub) headOf(n int) string {
	if st := g.pulls[n-1]; st.sha != "" {
		return st.sha
	}
	if g.headSHA == nil {
		return ""
	}
	branch, _ := g.created[n-1]["head"].(string)
	return g.headSHA(branch)
}

func (g *fakeGitHub) pullURL(n int) string {
	return g.srv.URL + "/acme/widgets/pull/" + strconv.Itoa(n)
}

func (g *fakeGitHub) feedbackJSON(n int, kind string) []map[string]any {
	out := []map[string]any{}
	for _, f := range g.feedback[n] {
		if f.kind != kind {
			continue
		}
		id := strconv.Itoa(f.id)
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(f.id) * time.Second).Format(time.RFC3339)
		item := map[string]any{"id": f.id, "user": map[string]any{"login": f.user}, "body": f.body}
		switch kind {
		case "review":
			item["state"] = f.state
			item["submitted_at"] = at
			item["html_url"] = g.pullURL(n) + "#pullrequestreview-" + id
		case "inline":
			item["path"], item["line"], item["created_at"] = f.path, f.line, at
			item["html_url"] = g.pullURL(n) + "#discussion_r" + id
		default:
			item["created_at"] = at
			item["html_url"] = g.pullURL(n) + "#issuecomment-" + id
		}
		out = append(out, item)
	}
	return out
}

func (g *fakeGitHub) add(n int, f fakeFeedback) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	f.id = g.nextID
	g.feedback[n] = append(g.feedback[n], f)
	return f.id
}

// AddComment adds a conversation comment and returns its ID.
func (g *fakeGitHub) AddComment(n int, user, body string) int {
	return g.add(n, fakeFeedback{kind: "comment", user: user, body: body})
}

// AddInlineComment adds a comment on a line of a file and returns its ID.
func (g *fakeGitHub) AddInlineComment(n int, user, body, path string, line int) int {
	return g.add(n, fakeFeedback{kind: "inline", user: user, body: body, path: path, line: line})
}

// AddReview adds a review in the given state (APPROVED, CHANGES_REQUESTED, COMMENTED) and returns its ID.
func (g *fakeGitHub) AddReview(n int, user, body, state string) int {
	return g.add(n, fakeFeedback{kind: "review", user: user, body: body, state: state})
}

// Close closes the pull request without merging it.
func (g *fakeGitHub) Close(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pulls[n-1].sha = g.headOf(n)
	g.pulls[n-1].closed = true
}

// Merge merges the pull request.
func (g *fakeGitHub) Merge(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pulls[n-1].sha = g.headOf(n)
	g.pulls[n-1].merged = true
}

func (g *fakeGitHub) requestCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.requests)
}

// writeCount counts the requests that were not reads.
func (g *fakeGitHub) writeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, r := range g.requests {
		if !strings.HasPrefix(r, http.MethodGet+" ") {
			n++
		}
	}
	return n
}

func (g *fakeGitHub) creates() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.created...)
}

type quickE2E struct {
	*e2eEnv
	gh   *fakeGitHub
	bare string
}

// newQuickE2E builds a repository with a bare origin, a fake claude on PATH
// and a fake GitHub, and points the binary at them.
func newQuickE2E(t *testing.T) *quickE2E {
	t.Helper()
	e := newE2EEnv(t)
	gh := newFakeGitHub(t)

	bin := t.TempDir()
	script := "#!/bin/sh\necho '2.1.325 (Claude Code)'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, kv := range e.env {
		if strings.HasPrefix(kv, "PATH=") {
			e.env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
	}
	e.env = append(e.env, "RALPH_GITHUB_API_URL="+gh.srv.URL, "GITHUB_TOKEN=dummy-token")

	bare := filepath.Join(t.TempDir(), "origin.git")
	e.repo.Git("init", "--bare", "-b", "main", bare)
	e.repo.Git("remote", "add", "origin", bare)
	e.repo.Git("push", "-u", "origin", "main")
	gh.headSHA = func(branch string) string {
		out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return &quickE2E{e2eEnv: e, gh: gh, bare: bare}
}

// squashMerge merges pull request n on the host the way a squash merge does:
// the base on the origin gets one new commit with the tree of the branch, and
// the branch's own commits are not ancestors of it.
func (q *quickE2E) squashMerge(t *testing.T, n int) {
	t.Helper()
	branch, _ := q.gh.creates()[n-1]["head"].(string)
	base, _ := q.gh.creates()[n-1]["base"].(string)
	git := func(args ...string) string {
		return q.repo.Git(append([]string{"-C", q.bare, "-c", "user.name=Host", "-c", "user.email=host@example.com"}, args...)...)
	}
	commit := git("commit-tree", "-p", "refs/heads/"+base, "-m", "Squash merge of #"+strconv.Itoa(n), "refs/heads/"+branch+"^{tree}")
	if err := exec.Command("git", "-C", q.bare, "merge-base", "--is-ancestor", "refs/heads/"+branch, "refs/heads/"+base).Run(); err == nil {
		t.Fatalf("%s is already part of %s: the merge would not be a squash", branch, base)
	}
	git("update-ref", "refs/heads/"+base, commit)
	q.gh.Merge(n)
}

// run executes ralph and returns its exit code and output.
func (q *quickE2E) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := q.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	default:
		t.Fatalf("ralph %s: %v", strings.Join(args, " "), err)
		return -1, "", ""
	}
}

func (q *quickE2E) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := q.run(t, args...)
	if code != 0 {
		t.Fatalf("ralph %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

// initAndCommit installs the claude-code integration with the verification
// settings, then commits and pushes everything but the local permission file.
func (q *quickE2E) initAndCommit(t *testing.T) {
	t.Helper()
	q.mustRun(t, "init", "--harness", "claude-code")
	path := filepath.Join(q.dir, ".ralph", "project.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, []byte(quickProjectConfig)...), 0o644); err != nil {
		t.Fatal(err)
	}
	q.repo.Git("add", "-A", "--", ".", ":(exclude).claude/settings.local.json")
	q.repo.Git("commit", "-m", "Install ralph")
	q.repo.Git("push", "origin", "main")
}

func (q *quickE2E) remoteHasBranch(branch string) bool {
	return q.repo.Git("ls-remote", "--heads", q.bare, branch) != ""
}

func TestE2EQuickViaCommandLine(t *testing.T) {
	q := newQuickE2E(t)
	q.initAndCommit(t)

	if got := q.repo.Git("status", "--short"); got != "?? .claude/settings.local.json" {
		t.Fatalf("git status after init = %q, want only the local permission file", got)
	}

	const branch = "ralph/t-1-add-feature-file"
	q.mustRun(t, "quick", "start", "Add feature file", "--criteria", "feature.txt exists")
	if got := q.repo.Git("rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("branch = %q, want %s", got, branch)
	}
	status := q.mustRun(t, "status")
	if !strings.Contains(status, "Unfinalized quick work") || !strings.Contains(status, "T-1") {
		t.Errorf("status lacks the unfinalized section: %q", status)
	}

	q.repo.WriteFile("sub/dir/extra.txt", "extra\n")
	code, _, stderr := q.run(t, "quick", "finish")
	if code != 1 {
		t.Errorf("finish before the file exists: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "test -f feature.txt") || !strings.Contains(stderr, "exit 1") {
		t.Errorf("stderr = %q, want the failing command and exit 1", stderr)
	}
	if q.remoteHasBranch(branch) {
		t.Error("remote has the branch after failed verification")
	}
	if n := q.gh.requestCount(); n != 0 {
		t.Errorf("fake GitHub saw %d requests, want none", n)
	}
	if status := q.mustRun(t, "status"); !strings.Contains(status, "T-1") || !strings.Contains(status, "Unfinalized quick work") {
		t.Errorf("status after failed finish = %q", status)
	}

	q.repo.WriteFile("feature.txt", "hello\n")
	out := q.mustRun(t, "quick", "finish", "--summary", "Added feature.txt")

	verifyAt := strings.Index(out, "verification (")
	deliveredAt := strings.Index(out, "delivered T-1")
	prAt := strings.Index(out, "pull request: ")
	if verifyAt < 0 || deliveredAt < verifyAt || prAt < deliveredAt {
		t.Errorf("output order wrong (verification %d, delivered %d, pull request %d): %q", verifyAt, deliveredAt, prAt, out)
	}
	for _, want := range []string{"test -f feature.txt", "echo verified", q.gh.srv.URL + "/acme/widgets/pull/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}

	if msg := q.repo.Git("log", "-1", "--format=%B", branch); msg != "T-1: Add feature file\n\nRalph-Task: T-1" {
		t.Errorf("commit message = %q", msg)
	}
	files := strings.Fields(q.repo.Git("show", "--format=", "--name-only", branch))
	if strings.Join(files, " ") != "feature.txt sub/dir/extra.txt" {
		t.Errorf("committed files = %v", files)
	}
	if !q.remoteHasBranch(branch) {
		t.Error("remote lacks the branch")
	}

	created := q.gh.creates()
	if len(created) != 1 {
		t.Fatalf("fake GitHub saw %d creates, want 1", len(created))
	}
	pr := created[0]
	if pr["head"] != branch || pr["base"] != "main" || pr["draft"] != true {
		t.Errorf("pull request = %v", pr)
	}
	body, _ := pr["body"].(string)
	for _, want := range []string{"feature.txt exists", "Added feature.txt", "echo verified: passed", "Opened by ralph for T-1."} {
		if !strings.Contains(body, want) {
			t.Errorf("pull request body lacks %q: %q", want, body)
		}
	}

	if all := q.mustRun(t, "status", "--all"); !strings.Contains(all, "T-1") || !strings.Contains(all, "DONE") {
		t.Errorf("status --all = %q", all)
	}
	if status := q.mustRun(t, "status"); strings.Contains(status, "Unfinalized quick work") {
		t.Errorf("status still lists unfinalized work: %q", status)
	}

	code, _, stderr = q.run(t, "quick", "finish", "T-1")
	if code == 0 || !strings.Contains(stderr, "not quick work in progress") {
		t.Errorf("second finish: exit %d, stderr %q", code, stderr)
	}
}

func TestE2EQuickViaMCP(t *testing.T) {
	q := newQuickE2E(t)
	q.initAndCommit(t)
	cs := q.connect(t, &bytes.Buffer{})

	const branch = "ralph/t-1-add-feature-file"
	startRes := e2eCall(t, cs, "quick_start", map[string]any{"title": "Add feature file", "acceptance_criteria": "feature.txt exists"})
	if startRes.IsError {
		t.Fatalf("quick_start: %s", e2eText(startRes))
	}
	started, _ := startRes.StructuredContent.(map[string]any)
	if started["branch"] != branch || started["base"] != "main" {
		t.Fatalf("quick_start = %v", started)
	}
	q.repo.WriteFile("feature.txt", "hello\n")

	// Notices attach to whichever call follows the change, once per session.
	first := e2eCall(t, cs, "status", nil)
	if first.IsError {
		t.Fatalf("status: %s", e2eText(first))
	}
	if n := strings.Count(e2eText(startRes)+e2eText(first), "Notice for the user:"); n != 1 {
		t.Errorf("notice text items = %d, want 1: %q %q", n, e2eText(startRes), e2eText(first))
	}
	if n := noticeCount(startRes, "quick_unfinalized") + noticeCount(first, "quick_unfinalized"); n != 1 {
		t.Errorf("quick_unfinalized notices = %d, want 1", n)
	}
	st, _ := first.StructuredContent.(map[string]any)
	if u, _ := st["unfinalized"].([]any); len(u) != 1 {
		t.Errorf("unfinalized = %v", st["unfinalized"])
	}

	second := e2eCall(t, cs, "status", nil)
	if strings.Contains(e2eText(second), "Notice for the user:") || noticeCount(second, "quick_unfinalized") != 0 {
		t.Errorf("second status repeats the notice: %q %v", e2eText(second), second.StructuredContent)
	}

	fin := e2eOK(t, cs, "quick_finish", map[string]any{"summary": "Added feature.txt"})
	pr, _ := fin["pull_request"].(map[string]any)
	if pr["url"] != q.gh.srv.URL+"/acme/widgets/pull/1" || pr["draft"] != true {
		t.Errorf("pull_request = %v", pr)
	}
	if fin["committed"] != true {
		t.Errorf("committed = %v", fin["committed"])
	}
	ver, _ := fin["verification"].(map[string]any)
	results, _ := ver["results"].([]any)
	if ver["configured"] != true || len(results) != 2 {
		t.Fatalf("verification = %v", ver)
	}
	for _, r := range results {
		if m, _ := r.(map[string]any); m["passed"] != true {
			t.Errorf("check = %v", m)
		}
	}
	if !q.remoteHasBranch(branch) {
		t.Error("remote lacks the branch")
	}
	if n := len(q.gh.creates()); n != 1 {
		t.Errorf("creates = %d, want 1", n)
	}

	after := statusOf(t, cs)
	if u, _ := after["unfinalized"].([]any); len(u) != 0 {
		t.Errorf("unfinalized after finish = %v", after["unfinalized"])
	}
}

func noticeCount(res *mcp.CallToolResult, code string) int {
	st, _ := res.StructuredContent.(map[string]any)
	list, _ := st["notices"].([]any)
	n := 0
	for _, item := range list {
		if m, _ := item.(map[string]any); m["code"] == code {
			n++
		}
	}
	return n
}

func TestE2EQuickStartRefusals(t *testing.T) {
	q := newQuickE2E(t)
	q.initAndCommit(t)

	q.repo.WriteFile("stray.txt", "x\n")
	code, _, stderr := q.run(t, "quick", "start", "Dirty start")
	if code != 1 || !strings.Contains(stderr, "stray.txt") {
		t.Errorf("dirty start: exit %d, stderr %q", code, stderr)
	}
	if err := os.Remove(filepath.Join(q.dir, "stray.txt")); err != nil {
		t.Fatal(err)
	}

	q.repo.Git("switch", "-c", "elsewhere")
	code, _, stderr = q.run(t, "quick", "start", "Wrong branch")
	if code != 1 || !strings.Contains(stderr, "main") {
		t.Errorf("start on another branch: exit %d, stderr %q", code, stderr)
	}

	var doc struct {
		Briefs []any `json:"briefs"`
		Tasks  []any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(q.mustRun(t, "status", "--json", "--all")), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tasks) != 0 || len(doc.Briefs) != 0 {
		t.Errorf("refused starts created units: %+v", doc)
	}
}

func TestE2EQuickCommandInstalled(t *testing.T) {
	q := newQuickE2E(t)
	q.mustRun(t, "init", "--harness", "claude-code")

	cmd, err := os.ReadFile(filepath.Join(q.dir, ".claude/commands/ralph/quick.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mcp__ralph__quick_start", "mcp__ralph__quick_finish"} {
		if !strings.Contains(string(cmd), want) {
			t.Errorf("quick.md lacks %s", want)
		}
	}
	claude, err := os.ReadFile(filepath.Join(q.dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), "/ralph:quick") {
		t.Errorf("CLAUDE.md does not list /ralph:quick:\n%s", claude)
	}
}
