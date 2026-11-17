package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const prVerifyConfig = `github:
  owner: acme
  repo: widgets
loop:
  harness: claude-code
verify:
  commands:
    - test -f feature.txt
    - echo verified
    - test ! -e .ralph/runs/fail-verify
`

// prFakeClaude is the loop's fake claude that also writes the files named by
// FAKE-WRITE lines quoted in a task body, which is how review feedback reaches it.
var prFakeClaude = strings.Replace(loopFakeClaude,
	`grep '^FAKE-WRITE ' "$dir/stdin-$n" | while read -r _ path; do`,
	`sed -n 's/^\(> \)\{0,1\}FAKE-WRITE //p' "$dir/stdin-$n" | while read -r path; do`, 1)

type prE2E struct {
	*loopE2E
	gh   *fakeGitHub
	bare string
	q    *quickE2E
}

// newPRE2E builds a repository with a bare origin, a fake GitHub and a fake
// claude that edits files, ready for ralph.
func newPRE2E(t *testing.T) *prE2E {
	t.Helper()
	if !strings.Contains(prFakeClaude, "sed -n") {
		t.Fatal("the fake claude was not adapted")
	}
	q := newQuickE2E(t)
	var fake string
	for _, kv := range q.env {
		if dir, ok := strings.CutPrefix(kv, "PATH="); ok {
			fake = strings.Split(dir, string(os.PathListSeparator))[0]
		}
	}
	if err := os.WriteFile(filepath.Join(fake, "claude"), []byte(prFakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	q.mustRun(t, "init", "--harness", "claude-code")
	path := filepath.Join(q.dir, ".ralph", "project.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, []byte(prVerifyConfig)...), 0o644); err != nil {
		t.Fatal(err)
	}
	q.repo.Git("add", "-A")
	q.repo.Git("commit", "-m", "Install ralph")
	q.repo.Git("push", "origin", "main")
	l := &loopE2E{e2eEnv: q.e2eEnv, fake: fake, cs: q.connect(t, &strings.Builder{})}
	return &prE2E{loopE2E: l, gh: q.gh, bare: q.bare, q: q}
}

// deliver seeds B-1 and S-1 with two ready tasks and has the loop deliver them.
func (p *prE2E) deliver(t *testing.T) {
	t.Helper()
	p.seedSlice(t)
	for _, name := range []string{"feature", "extra"} {
		made := e2eOK(t, p.cs, "unit_create", map[string]any{
			"level": "task", "parent_id": "S-1", "title": "Write " + name,
			"body":                "Create the file " + name + ".txt.\nFAKE-WRITE " + name + ".txt",
			"acceptance_criteria": name + ".txt exists",
		})
		e2eOK(t, p.cs, "task_set_state", map[string]any{"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready"})
	}
	r := p.mustRalph(t, "loop", "S-1")
	wantContains(t, "loop output", r.stdout, "S-1 done")
	if got := p.branch(); got != loopBranch {
		t.Fatalf("branch after the loop = %q, want %s", got, loopBranch)
	}
}

// open delivers the slice and opens its pull request, number 1.
func (p *prE2E) open(t *testing.T) {
	t.Helper()
	p.deliver(t)
	p.mustRalph(t, "pr", "create", "S-1")
	if n := len(p.gh.creates()); n != 1 {
		t.Fatalf("pull requests created = %d, want 1", n)
	}
}

func (p *prE2E) unit(t *testing.T, id string) map[string]any {
	t.Helper()
	return e2eOK(t, p.cs, "unit_get", map[string]any{"id": id})
}

type prNote struct{ kind, body string }

func (p *prE2E) typedNotes(t *testing.T, id string) []prNote {
	t.Helper()
	var out []prNote
	list, _ := p.unit(t, id)["notes"].([]any)
	for _, n := range list {
		m := n.(map[string]any)
		out = append(out, prNote{kind: m["kind"].(string), body: m["body"].(string)})
	}
	return out
}

func (p *prE2E) hasNote(t *testing.T, id, kind, body string) bool {
	t.Helper()
	for _, n := range p.typedNotes(t, id) {
		if n.kind == kind && n.body == body {
			return true
		}
	}
	return false
}

func (p *prE2E) linkTargets(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	list, _ := p.unit(t, id)["links"].([]any)
	for _, l := range list {
		out = append(out, l.(map[string]any)["target"].(string))
	}
	return out
}

func (p *prE2E) pullRequests(t *testing.T, id string) []map[string]any {
	t.Helper()
	var out []map[string]any
	list, _ := p.unit(t, id)["pull_requests"].([]any)
	for _, r := range list {
		out = append(out, r.(map[string]any))
	}
	return out
}

func (p *prE2E) children(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	list, _ := p.unit(t, id)["children"].([]any)
	for _, c := range list {
		m := c.(map[string]any)
		out = append(out, m["id"].(string)+" "+m["status"].(string))
	}
	return out
}

func (p *prE2E) remoteHead(t *testing.T, branch string) string {
	t.Helper()
	fields := strings.Fields(p.repo.Git("ls-remote", "--heads", p.bare, branch))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (p *prE2E) firstLine(t *testing.T, args ...string) string {
	t.Helper()
	r := p.mustRalph(t, args...)
	line, _, _ := strings.Cut(r.stdout, "\n")
	return line
}

func (p *prE2E) refuse(t *testing.T, args ...string) e2eRun {
	t.Helper()
	r := p.run(t, args...)
	wantExit(t, r, 1)
	return r
}

const needsDecision = "S-1 needs a decision: pull request #1 (%s) was closed without merging. " +
	"Choose one: ralph pr reopen S-1, ralph pr rearticulate S-1 or ralph pr cancel S-1"

func TestE2EPRCreateOpensDraftPullRequest(t *testing.T) {
	p := newPRE2E(t)
	p.deliver(t)
	if got := p.pullRequests(t, "S-1"); len(got) != 0 {
		t.Fatalf("pull requests before create = %v", got)
	}

	p.repo.Git("switch", "main")
	r := p.refuse(t, "pr", "create", "S-1")
	wantContains(t, "stderr", r.stderr, "S-1 is on branch "+loopBranch+" but main is checked out: run git switch "+loopBranch)
	p.repo.Git("switch", loopBranch)

	p.repo.WriteFile("stray.txt", "x\n")
	r = p.refuse(t, "pr", "create", "S-1")
	wantContains(t, "stderr", r.stderr, "stray.txt")
	if err := os.Remove(filepath.Join(p.dir, "stray.txt")); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(p.dir, ".ralph", "runs", "fail-verify")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r = p.refuse(t, "pr", "create", "S-1")
	wantContains(t, "stderr", r.stderr, "test ! -e .ralph/runs/fail-verify", "exit 1")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if p.remoteHead(t, loopBranch) != "" {
		t.Error("a refused or failed create pushed the branch")
	}
	if n := p.gh.requestCount(); n != 0 {
		t.Errorf("the host saw %d requests before the create succeeded", n)
	}

	r = p.mustRalph(t, "pr", "create", "S-1")
	url := p.gh.pullURL(1)
	wantContains(t, "output", r.stdout, "pushed "+loopBranch+" to origin", "verification: test -f feature.txt (passed)",
		"verification: echo verified (passed)", "pull request: "+url+" (draft)", "nothing has been merged: a person reviews the pull request")
	if got, want := p.remoteHead(t, loopBranch), p.repo.Git("rev-parse", "HEAD"); got != want {
		t.Errorf("origin branch = %q, want %q", got, want)
	}

	created := p.gh.creates()
	if len(created) != 1 {
		t.Fatalf("pull requests created = %d, want 1", len(created))
	}
	pr := created[0]
	if pr["title"] != "S-1: Deliver the files" || pr["draft"] != true || pr["head"] != loopBranch || pr["base"] != "main" {
		t.Errorf("pull request = %v", pr)
	}
	body, _ := pr["body"].(string)
	want := []string{
		"Part of B-1: Ship the files.",
		"Deliver them one by one.",
		"## Tasks",
		"- T-1: Write feature (" + p.commitLinks(t, "T-1")[0][:12] + ")",
		"- T-2: Write extra (" + p.commitLinks(t, "T-2")[0][:12] + ")",
		"## Verification",
		"- test -f feature.txt: passed (",
		"- echo verified: passed (",
		"Opened by ralph for S-1.",
	}
	last := -1
	for _, w := range want {
		at := strings.Index(body, w)
		if at < 0 || at < last {
			t.Errorf("body lacks %q in order:\n%s", w, body)
		}
		last = at
	}

	if links := p.linkTargets(t, "S-1"); !contains(links, "pr:"+url) {
		t.Errorf("slice links = %v, want pr:%s", links, url)
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request opened: "+url) {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}
	if prs := p.pullRequests(t, "S-1"); len(prs) != 1 || prs[0]["number"] != float64(1) || prs[0]["state"] != "open" || prs[0]["url"] != url {
		t.Errorf("pull requests = %v", prs)
	}

	p.repo.WriteFile("later.txt", "later\n")
	p.repo.Git("add", "later.txt")
	p.repo.Git("commit", "-m", "A fix after review")
	if p.remoteHead(t, loopBranch) == p.repo.Git("rev-parse", "HEAD") {
		t.Fatal("the origin already has the new commit")
	}
	r = p.mustRalph(t, "pr", "create", "S-1")
	wantContains(t, "second create output", r.stdout, "pull request: "+url+" (reused)")
	if got, want := p.remoteHead(t, loopBranch), p.repo.Git("rev-parse", "HEAD"); got != want {
		t.Errorf("origin branch after the second create = %q, want %q", got, want)
	}
	if n := len(p.gh.creates()); n != 1 {
		t.Errorf("a second pull request was opened: %d", n)
	}
	if prs := p.pullRequests(t, "S-1"); len(prs) != 1 {
		t.Errorf("pull requests after reuse = %v", prs)
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request reused: "+url) {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}

	status := p.mustRalph(t, "status", "S-1").stdout
	wantContains(t, "status", status, "S-1  IN_REVIEW  Deliver the files", "Pull requests:", "#1  open  "+url)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestE2EPRFeedbackCreatesTasksTheLoopWorks(t *testing.T) {
	p := newPRE2E(t)
	p.open(t)
	url := p.gh.pullURL(1)

	p.gh.AddInlineComment(1, "alice", "Rename the value.\nFAKE-WRITE renamed.txt", "feature.txt", 1)
	p.gh.AddReview(1, "bob", "Handle the error case.\nFAKE-WRITE errors.txt", "CHANGES_REQUESTED")
	p.gh.AddComment(1, "carol", "Thanks, looks fine overall.")
	p.gh.AddReview(1, "dave", "", "APPROVED")

	r := p.mustRalph(t, "pr", "feedback", "S-1")
	wantContains(t, "feedback", r.stdout,
		"S-1 pull request #1 (open): "+url,
		"feedback: 2 new, 0 already handled, 2 not turned into tasks",
		"inline  inline-1  alice  feature.txt:1  Rename the value. FAKE-WRITE renamed.txt  would create",
		"review  review-2  bob  -  Handle the error case. FAKE-WRITE errors.txt  would create",
		"comment  comment-3  carol  -  Thanks, looks fine overall.  not a task",
		"review  review-4  dave  -  -  not a task")
	if got := p.children(t, "S-1"); strings.Join(got, ",") != "T-1 DONE,T-2 DONE" {
		t.Errorf("tasks after the read = %v", got)
	}

	r = p.mustRalph(t, "pr", "feedback", "S-1", "--create")
	wantContains(t, "created", r.stdout, "created T-3, T-4")
	if got := p.children(t, "S-1"); strings.Join(got, ",") != "T-1 DONE,T-2 DONE,T-3 TODO,T-4 TODO" {
		t.Errorf("tasks after --create = %v", got)
	}
	criteria := func(id string) string {
		return "- The change the reviewer asks for in " + id + " is made, or the final answer says why it was not.\n- Every verification command passes."
	}
	instruction := "Make the change the reviewer asks for. If you decide not to, say why in your final answer."
	t3, t4 := p.unit(t, "T-3"), p.unit(t, "T-4")
	if t3["title"] != "Address alice's comment on feature.txt:1" {
		t.Errorf("T-3 title = %q", t3["title"])
	}
	wantBody3 := "alice left this feedback on the pull request " + url + ":\n\n> Rename the value.\n> FAKE-WRITE renamed.txt\n\n" +
		"Location: feature.txt:1\nFeedback: " + url + "#discussion_r1\n\n" + instruction
	if t3["body"] != wantBody3 {
		t.Errorf("T-3 body = %q, want %q", t3["body"], wantBody3)
	}
	if strings.TrimSpace(t3["acceptance_criteria"].(string)) != criteria("inline-1") {
		t.Errorf("T-3 criteria = %q", t3["acceptance_criteria"])
	}
	if t4["title"] != "Address changes requested by bob" {
		t.Errorf("T-4 title = %q", t4["title"])
	}
	wantBody4 := "bob left this feedback on the pull request " + url + ":\n\n> Handle the error case.\n> FAKE-WRITE errors.txt\n\n" +
		"Feedback: " + url + "#pullrequestreview-2\n\n" + instruction
	if t4["body"] != wantBody4 {
		t.Errorf("T-4 body = %q, want %q", t4["body"], wantBody4)
	}
	if strings.TrimSpace(t4["acceptance_criteria"].(string)) != criteria("review-2") {
		t.Errorf("T-4 criteria = %q", t4["acceptance_criteria"])
	}
	if links := p.linkTargets(t, "T-3"); !contains(links, "feedback:1:inline-1") {
		t.Errorf("T-3 links = %v", links)
	}
	if links := p.linkTargets(t, "T-4"); !contains(links, "feedback:1:review-2") {
		t.Errorf("T-4 links = %v", links)
	}
	if !p.hasNote(t, "T-3", "NOTE", "Created from review feedback "+url+"#discussion_r1") {
		t.Errorf("T-3 notes = %v", p.typedNotes(t, "T-3"))
	}
	if got := p.status(t, "S-1"); got != "READY" {
		t.Errorf("slice = %s, want READY", got)
	}

	r = p.mustRalph(t, "pr", "feedback", "S-1", "--create")
	wantContains(t, "second create", r.stdout, "no new tasks", "feedback: 0 new, 2 already handled, 2 not turned into tasks",
		"inline  inline-1  alice  feature.txt:1  Rename the value. FAKE-WRITE renamed.txt  T-3",
		"review  review-2  bob  -  Handle the error case. FAKE-WRITE errors.txt  T-4")
	if got := p.children(t, "S-1"); len(got) != 4 {
		t.Errorf("tasks after the second --create = %v", got)
	}

	r = p.mustRalph(t, "pr", "feedback", "S-1", "--all")
	wantContains(t, "--all", r.stdout, "feedback: 1 new, 2 already handled, 1 not turned into tasks",
		"comment  comment-3  carol  -  Thanks, looks fine overall.  would create", "review  review-4  dave  -  -  not a task")

	p.mustRalph(t, "loop", "S-1")
	if got := p.branch(); got != loopBranch {
		t.Errorf("branch = %q", got)
	}
	for id, file := range map[string]string{"T-3": "renamed.txt", "T-4": "errors.txt"} {
		if s := p.status(t, id); s != "DONE" {
			t.Errorf("%s = %s", id, s)
		}
		links := p.commitLinks(t, id)
		if len(links) != 1 {
			t.Fatalf("%s commit links = %v", id, links)
		}
		if got := p.repo.Git("show", "--format=", "--name-only", links[0]); got != file {
			t.Errorf("%s commit files = %q, want %s", id, got, file)
		}
	}
	msgs := p.repo.Git("log", "--reverse", "--format=%s", "main..HEAD")
	if want := strings.Join([]string{"T-1: Write feature", "T-2: Write extra",
		"T-3: Address alice's comment on feature.txt:1", "T-4: Address changes requested by bob"}, "\n"); msgs != want {
		t.Errorf("commits = %q, want %q", msgs, want)
	}
	if got := p.status(t, "S-1"); got != "IN_REVIEW" {
		t.Errorf("slice after the loop = %s", got)
	}

	r = p.mustRalph(t, "pr", "create", "S-1")
	wantContains(t, "create", r.stdout, "pull request: "+url+" (reused)")
	if got, want := p.remoteHead(t, loopBranch), p.repo.Git("rev-parse", "HEAD"); got != want {
		t.Errorf("origin branch = %q, want %q", got, want)
	}
	if n := len(p.gh.creates()); n != 1 {
		t.Errorf("pull requests created = %d", n)
	}

	r = p.mustRalph(t, "pr", "feedback", "S-1", "--all", "--create")
	wantContains(t, "--all --create", r.stdout, "created T-5", "review  review-4  dave  -  -  not a task")
	if got := p.unit(t, "T-5")["title"]; got != "Respond to carol's comment" {
		t.Errorf("T-5 title = %q", got)
	}
	if got := p.children(t, "S-1"); len(got) != 5 {
		t.Errorf("tasks = %v: the approval must never become a task", got)
	}
}

func TestE2EPRClosedWithoutMergingStopsTheSlice(t *testing.T) {
	p := newPRE2E(t)
	p.open(t)
	url := p.gh.pullURL(1)
	p.gh.Close(1)

	r := p.mustRalph(t, "pr", "check", "S-1")
	wantContains(t, "check", r.stdout, "S-1 pull request #1 (closed): "+url, "closed without merging: S-1 needs a decision",
		"  ralph pr reopen S-1", "  ralph pr rearticulate S-1", "  ralph pr cancel S-1")
	if prs := p.pullRequests(t, "S-1"); len(prs) != 1 || prs[0]["state"] != "closed" || prs[0]["resolution"] != "" {
		t.Errorf("pull requests = %v", prs)
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request "+url+" was closed without merging.") {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}

	if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  NEEDS_ATTENTION  ") {
		t.Errorf("slice status line = %q", line)
	}
	if line := p.firstLine(t, "status", "B-1"); !strings.HasPrefix(line, "B-1  NEEDS_ATTENTION  ") {
		t.Errorf("brief status line = %q", line)
	}
	wantContains(t, "status", p.mustRalph(t, "status").stdout, "NEEDS_ATTENTION")

	r = p.mustRalph(t, "pr", "check", "S-1")
	wantContains(t, "second check", r.stdout, "unchanged")

	calls := p.fakeCalls()
	want := fmt.Sprintf(needsDecision, url)
	for _, args := range [][]string{{"loop", "S-1"}, {"pr", "create", "S-1"}} {
		r = p.refuse(t, args...)
		wantContains(t, strings.Join(args, " ")+" stderr", r.stderr, want)
	}
	if n := len(p.gh.creates()); n != 1 || p.fakeCalls() != calls {
		t.Errorf("a refused command acted: %d pull requests, %d harness calls (was %d)", n, p.fakeCalls(), calls)
	}

	e2eOK(t, p.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Later work"})
	made := e2eOK(t, p.cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-2", "title": "Later task", "body": "Later.", "acceptance_criteria": "done"})
	e2eOK(t, p.cs, "task_set_state", map[string]any{"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready"})
	e2eOK(t, p.cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	r = p.refuse(t, "loop", "S-2")
	wantContains(t, "waiting slice stderr", r.stderr, "S-2 waits for S-1 (Deliver the files)")
	if p.fakeCalls() != calls || p.status(t, "T-3") != "TODO" {
		t.Error("the waiting slice started a task")
	}

	p.mustRalph(t, "pr", "reopen", "S-1")
	p.gh.Merge(2)
	before := p.children(t, "S-1")
	r = p.mustRalph(t, "pr", "check", "S-1")
	url2 := p.gh.pullURL(2)
	wantContains(t, "merged check", r.stdout, "S-1 pull request #2 (merged): "+url2, "merged")
	if strings.Contains(r.stdout, "needs a decision") {
		t.Errorf("a merge asked for a decision: %s", r.stdout)
	}
	prs := p.pullRequests(t, "S-1")
	if len(prs) != 2 || prs[0]["resolution"] != "reopened" || prs[0]["state"] != "closed" || prs[1]["state"] != "merged" || prs[1]["resolution"] != "" {
		t.Errorf("pull requests = %v", prs)
	}
	if after := p.children(t, "S-1"); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("tasks changed: %v -> %v", before, after)
	}
	if got := p.status(t, "S-1"); got != "MERGED" {
		t.Errorf("slice after the merge = %s", got)
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request "+url2+" was merged.") {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}
	if got := p.mustRalph(t, "pr", "check", "S-1").stdout; !strings.Contains(got, "unchanged") {
		t.Errorf("check after the merge was recorded: %s", got)
	}
}

func TestE2EPRReopen(t *testing.T) {
	p := newPRE2E(t)
	p.open(t)
	url1, url2 := p.gh.pullURL(1), p.gh.pullURL(2)
	p.gh.Close(1)
	p.mustRalph(t, "pr", "check", "S-1")

	r := p.mustRalph(t, "pr", "reopen", "S-1")
	wantContains(t, "reopen", r.stdout, "pull request: "+url2+" (draft)", "resolved pull request #1 as reopened")
	created := p.gh.creates()
	if len(created) != 2 || created[1]["head"] != loopBranch || created[1]["base"] != "main" || created[1]["title"] != "S-1: Deliver the files" {
		t.Fatalf("pull requests created = %v", created)
	}
	prs := p.pullRequests(t, "S-1")
	if len(prs) != 2 || prs[0]["state"] != "closed" || prs[0]["resolution"] != "reopened" || prs[1]["state"] != "open" || prs[1]["number"] != float64(2) {
		t.Errorf("pull requests = %v", prs)
	}
	status := p.mustRalph(t, "status", "S-1").stdout
	wantContains(t, "status", status, "S-1  IN_REVIEW  ", "#1  closed  "+url1+"  (reopened)", "#2  open  "+url2)
	if strings.Contains(status, "NEEDS_ATTENTION") || strings.Contains(p.mustRalph(t, "status").stdout, "NEEDS_ATTENTION") {
		t.Error("the slice still needs attention")
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request "+url1+" was closed without merging; reopened as "+url2+".") {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}

	r = p.refuse(t, "pr", "reopen", "S-1")
	wantContains(t, "second reopen", r.stderr, "S-1 is not waiting for a decision")
	if n := len(p.gh.creates()); n != 2 {
		t.Errorf("pull requests created = %d, want 2", n)
	}
}

// stepBack opens the pull request, adds two feedback items that become the
// unfinished tasks T-3 and T-4, and closes the pull request.
func (p *prE2E) stepBack(t *testing.T) {
	t.Helper()
	p.open(t)
	p.gh.AddInlineComment(1, "alice", "Rename the value.", "feature.txt", 1)
	p.gh.AddReview(1, "bob", "Handle the error case.", "CHANGES_REQUESTED")
	p.mustRalph(t, "pr", "feedback", "S-1", "--create")
	p.gh.Close(1)
	p.mustRalph(t, "pr", "check", "S-1")
}

func TestE2EPRRearticulate(t *testing.T) {
	p := newPRE2E(t)
	p.stepBack(t)
	url := p.gh.pullURL(1)
	if got := p.status(t, "S-1"); got != "NEEDS_ATTENTION" {
		t.Fatalf("slice = %s", got)
	}
	calls := p.fakeCalls()

	r := p.mustRalph(t, "pr", "rearticulate", "S-1")
	wantContains(t, "rearticulate", r.stdout, "moved T-3, T-4 back to PROPOSED", "kept T-1, T-2 (done)",
		"resolved pull request #1 as rearticulated",
		"plan S-1 again: /ralph:receive B-1 to revise the plan, or /ralph:articulate S-1 to work out the tasks (/ralph/receive and /ralph/articulate in OpenCode)")
	reason := "pull request " + url + " was closed without merging; plan this slice again"
	for _, id := range []string{"T-3", "T-4"} {
		if s := p.status(t, id); s != "PROPOSED" {
			t.Errorf("%s = %s", id, s)
		}
		if !p.hasNote(t, id, "KICKBACK", "TODO to PROPOSED: "+reason) {
			t.Errorf("%s notes = %v, want a KICKBACK with %q", id, p.typedNotes(t, id), reason)
		}
	}
	for _, id := range []string{"T-1", "T-2"} {
		if s := p.status(t, id); s != "DONE" {
			t.Errorf("%s = %s", id, s)
		}
	}
	prs := p.pullRequests(t, "S-1")
	if len(prs) != 1 || prs[0]["state"] != "closed" || prs[0]["resolution"] != "rearticulated" {
		t.Errorf("pull requests = %v", prs)
	}
	if got := p.status(t, "S-1"); got == "NEEDS_ATTENTION" {
		t.Errorf("slice = %s", got)
	}
	if !p.hasNote(t, "S-1", "NOTE", "Pull request "+url+" was closed without merging; plan this slice again. Moved back to PROPOSED: T-3, T-4.") {
		t.Errorf("slice notes = %v", p.typedNotes(t, "S-1"))
	}

	r = p.run(t, "loop", "S-1")
	wantExit(t, r, 2)
	wantContains(t, "loop output", r.stdout, "S-1 stopped: T-3 is PROPOSED: it must be articulated and made TODO before the loop can run it")
	if p.fakeCalls() != calls {
		t.Error("the loop ran the harness")
	}

	r = p.refuse(t, "pr", "rearticulate", "S-1")
	wantContains(t, "second rearticulate", r.stderr, "S-1 is not waiting for a decision")
}

func TestE2EPRCancel(t *testing.T) {
	// setup leaves S-2 waiting for S-1 and S-3 waiting for S-2, each with one ready task.
	setup := func(t *testing.T) *prE2E {
		p := newPRE2E(t)
		p.stepBack(t)
		e2eOK(t, p.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Second slice"})
		e2eOK(t, p.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Third slice"})
		for _, pair := range [][2]string{{"S-2", "S-1"}, {"S-3", "S-2"}} {
			slice, deps := pair[0], pair[1]
			made := e2eOK(t, p.cs, "unit_create", map[string]any{"level": "task", "parent_id": slice, "title": "Work of " + slice, "body": "Work.", "acceptance_criteria": "done"})
			e2eOK(t, p.cs, "task_set_state", map[string]any{"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready"})
			e2eOK(t, p.cs, "dependency_add", map[string]any{"id": slice, "depends_on": deps})
		}
		return p
	}
	taskOf := func(t *testing.T, p *prE2E, slice string) string {
		return strings.Fields(p.children(t, slice)[0])[0]
	}
	cancelReason := func(p *prE2E) string {
		return "pull request " + p.gh.pullURL(1) + " was closed without merging; slice cancelled"
	}

	t.Run("without yes nothing changes", func(t *testing.T) {
		p := setup(t)
		for _, args := range [][]string{{"pr", "cancel", "S-1"}, {"pr", "cancel", "S-1", "--dependents"}} {
			r := p.mustRalph(t, args...)
			wantContains(t, "output", r.stdout, "would cancel T-3, T-4", "run again with --yes to do it")
			if strings.Contains(r.stdout, "resolved") {
				t.Errorf("%v resolved the pull request: %s", args, r.stdout)
			}
		}
		if r := p.mustRalph(t, "pr", "cancel", "S-1"); !strings.Contains(r.stdout, "slices waiting for S-1: S-2, S-3; they stay waiting, or add --dependents") {
			t.Errorf("without --dependents: %s", r.stdout)
		}
		if r := p.mustRalph(t, "pr", "cancel", "S-1", "--dependents"); !strings.Contains(r.stdout, "would also cancel the unfinished tasks of S-2, S-3") {
			t.Errorf("with --dependents: %s", r.stdout)
		}
		for _, id := range []string{"T-3", "T-4"} {
			if s := p.status(t, id); s != "TODO" {
				t.Errorf("%s = %s", id, s)
			}
		}
		if s := p.status(t, "S-1"); s != "NEEDS_ATTENTION" {
			t.Errorf("slice = %s", s)
		}
		if prs := p.pullRequests(t, "S-1"); prs[0]["resolution"] != "" {
			t.Errorf("pull requests = %v", prs)
		}
	})

	t.Run("yes cancels the slice and leaves dependents waiting", func(t *testing.T) {
		p := setup(t)
		r := p.mustRalph(t, "pr", "cancel", "S-1", "--yes")
		wantContains(t, "output", r.stdout, "cancelled T-3, T-4", "slices waiting for S-1: S-2, S-3; they stay waiting, or add --dependents",
			"resolved pull request #1 as cancelled")
		for _, id := range []string{"T-3", "T-4"} {
			if s := p.status(t, id); s != "CANCELLED" {
				t.Errorf("%s = %s", id, s)
			}
			if !p.hasNote(t, id, "NOTE", "TODO to CANCELLED: "+cancelReason(p)) {
				t.Errorf("%s notes = %v, want the reason %q", id, p.typedNotes(t, id), cancelReason(p))
			}
		}
		for _, id := range []string{"T-1", "T-2"} {
			if s := p.status(t, id); s != "DONE" {
				t.Errorf("%s = %s", id, s)
			}
		}
		if s := p.status(t, "S-1"); s != "CANCELLED" {
			t.Errorf("slice = %s", s)
		}
		for _, slice := range []string{"S-2", "S-3"} {
			if s := p.status(t, taskOf(t, p, slice)); s != "TODO" {
				t.Errorf("task of %s = %s, want it still waiting", slice, s)
			}
		}
		if prs := p.pullRequests(t, "S-1"); prs[0]["resolution"] != "cancelled" {
			t.Errorf("pull requests = %v", prs)
		}
		wantContains(t, "status", p.mustRalph(t, "status", "S-1").stdout, "S-1  CANCELLED  ", "(cancelled)")
		r = p.refuse(t, "pr", "cancel", "S-1", "--yes")
		wantContains(t, "second cancel", r.stderr, "S-1 is not waiting for a decision")
	})

	t.Run("dependents are cancelled too", func(t *testing.T) {
		p := setup(t)
		r := p.mustRalph(t, "pr", "cancel", "S-1", "--dependents", "--yes")
		wantContains(t, "output", r.stdout, "cancelled T-3, T-4", "also cancelled the unfinished tasks of S-2, S-3", "resolved pull request #1 as cancelled")
		for _, slice := range []string{"S-2", "S-3"} {
			id := taskOf(t, p, slice)
			if s := p.status(t, id); s != "CANCELLED" {
				t.Errorf("task %s of %s = %s", id, slice, s)
			}
			reason := "TODO to CANCELLED: S-1 was cancelled; this slice waited for it"
			if !p.hasNote(t, id, "NOTE", reason) {
				t.Errorf("%s notes = %v, want the reason %q", id, p.typedNotes(t, id), reason)
			}
		}
	})
}

func TestE2EPRNoDatabaseNoToken(t *testing.T) {
	commands := [][]string{
		{"pr", "create", "S-1"}, {"pr", "feedback", "S-1"}, {"pr", "check", "S-1"},
		{"pr", "reopen", "S-1"}, {"pr", "rearticulate", "S-1"}, {"pr", "cancel", "S-1"},
	}

	t.Run("before init", func(t *testing.T) {
		e := newE2EEnv(t)
		for _, args := range commands {
			r := e.run(t, args...)
			wantExit(t, r, 1)
			wantContains(t, strings.Join(args, " "), r.stderr, errNoDatabase)
		}
	})

	t.Run("outside a repository", func(t *testing.T) {
		e := newE2EEnv(t)
		for _, args := range commands {
			cmd := e.command(context.Background(), args...)
			cmd.Dir = t.TempDir()
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if err == nil || !asExit(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("ralph %v: %v\n%s", args, err, out)
			}
			wantContains(t, strings.Join(args, " "), string(out), errNotInRepository.Error())
		}
	})

	t.Run("missing token", func(t *testing.T) {
		p := newPRE2E(t)
		p.open(t)
		var kept []string
		for _, kv := range p.env {
			if !strings.HasPrefix(kv, "GITHUB_TOKEN=") {
				kept = append(kept, kv)
			}
		}
		p.env = kept
		for _, args := range [][]string{{"pr", "create", "S-1"}, {"pr", "feedback", "S-1"}, {"pr", "check", "S-1"}} {
			r := p.refuse(t, args...)
			wantContains(t, strings.Join(args, " "), r.stderr, `secret setting "github.token"`, "GITHUB_TOKEN")
		}
	})
}
