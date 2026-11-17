package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	nextBranch    = "ralph/s-2-second-slice"
	applyHintLine = "nothing was applied; name what to apply with --close, --feedback or --continue, or set follow_up.auto"
	footerPlural  = " follow-ups; ralph apply applies the ones named by the follow_up.auto setting or by flags"
)

// settingBoth makes ralph delete the remote branch of a merged slice too.
const settingBoth = "follow_up:\n  delete_branches: both\n"

// userAuto is the user file that names the kinds ralph apply applies by itself.
func userAuto(kinds ...string) string {
	s := "follow_up:\n  auto:\n"
	for _, k := range kinds {
		s += "    - " + k + "\n"
	}
	return s
}

func (p *prE2E) envValue(name string) string {
	for _, kv := range p.env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

func (p *prE2E) withoutToken() {
	var kept []string
	for _, kv := range p.env {
		if !strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			kept = append(kept, kv)
		}
	}
	p.env = kept
}

// userConfig writes the user's settings file.
func (p *prE2E) userConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := filepath.Join(p.envValue("XDG_CONFIG_HOME"), "ralph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// projectSetting adds settings to the committed project file and pushes them,
// before the slice is delivered, so the branch and the merged base carry them.
func (p *prE2E) projectSetting(t *testing.T, yaml string) {
	t.Helper()
	path := filepath.Join(p.dir, ".ralph", "project.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, []byte(yaml)...), 0o644); err != nil {
		t.Fatal(err)
	}
	p.repo.Git("add", "-A")
	p.repo.Git("commit", "-m", "Set follow-up settings")
	p.repo.Git("push", "origin", "main")
}

// addSecond creates S-2 in the brief with one ready task that writes second.txt;
// with waits it depends on S-1.
func (p *prE2E) addSecond(t *testing.T, waits bool) {
	t.Helper()
	e2eOK(t, p.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Second slice", "body": "Deliver the second file."})
	made := e2eOK(t, p.cs, "unit_create", map[string]any{
		"level": "task", "parent_id": "S-2", "title": "Write second",
		"body":                "Create the file second.txt.\nFAKE-WRITE second.txt",
		"acceptance_criteria": "second.txt exists",
	})
	e2eOK(t, p.cs, "task_set_state", map[string]any{"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready"})
	if waits {
		e2eOK(t, p.cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	}
}

// merged opens the pull request of S-1, adds S-2 waiting for it and merges the
// pull request on the host as a squash merge.
func (p *prE2E) merged(t *testing.T) {
	t.Helper()
	p.open(t)
	p.addSecond(t, true)
	p.q.squashMerge(t, 1)
}

func (p *prE2E) onMain(t *testing.T) {
	t.Helper()
	p.repo.Git("switch", "main")
}

func (p *prE2E) hasLocalBranch(branch string) bool {
	return p.repo.Git("branch", "--list", branch) != ""
}

func (p *prE2E) noteCount(t *testing.T, id, prefix string) int {
	t.Helper()
	n := 0
	for _, note := range p.typedNotes(t, id) {
		if strings.HasPrefix(note.body, prefix) {
			n++
		}
	}
	return n
}

// snapshot hashes every file outside .git, the database included, and lists
// the local refs and the host's refs.
func (p *prE2E) snapshot(t *testing.T) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(p.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(p.dir, path)
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		lines = append(lines, rel+" "+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	refs := p.repo.Git("for-each-ref") + "\nHEAD " + p.repo.Git("rev-parse", "HEAD") + "\n" + p.repo.Git("-C", p.bare, "for-each-ref")
	return strings.Join(lines, "\n") + "\n" + refs
}

func (p *prE2E) hasDatabase(t *testing.T) bool {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(p.dir, ".ralph", "store.db*"))
	return len(matches) > 0
}

type watchRun struct {
	cmd         *exec.Cmd
	stdout, err *lockedBuffer
	done        chan struct{}
	waitErr     error
}

// watch starts a long-running ralph command in the background.
func (e *e2eEnv) watch(t *testing.T, args ...string) *watchRun {
	t.Helper()
	w := &watchRun{stdout: &lockedBuffer{}, err: &lockedBuffer{}, done: make(chan struct{})}
	w.cmd = e.command(context.Background(), args...)
	w.cmd.Stdout, w.cmd.Stderr = w.stdout, w.err
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		w.waitErr = w.cmd.Wait()
		close(w.done)
	}()
	t.Cleanup(func() {
		select {
		case <-w.done:
		default:
			_ = w.cmd.Process.Kill()
			<-w.done
		}
	})
	return w
}

// waitFor waits until the output holds want at least n times.
func (w *watchRun) waitFor(t *testing.T, want string, n int) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(w.stdout.String(), want) >= n {
			return
		}
		select {
		case <-w.done:
			t.Fatalf("ralph ended before the output held %q %d times: %v\nstdout: %s\nstderr: %s", want, n, w.waitErr, w.stdout, w.err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("the output never held %q %d times:\n%s", want, n, w.stdout)
}

// interrupt sends SIGINT and returns the exit status.
func (w *watchRun) interrupt(t *testing.T) int {
	t.Helper()
	if err := w.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.done:
	case <-time.After(20 * time.Second):
		t.Fatal("ralph did not stop on SIGINT")
	}
	var exit *exec.ExitError
	switch {
	case w.waitErr == nil:
		return 0
	case errors.As(w.waitErr, &exit):
		return exit.ExitCode()
	}
	t.Fatal(w.waitErr)
	return -1
}

var monitorHeader = regexp.MustCompile(`^ralph monitor  \d{4}-\d\d-\d\d \d\d:\d\d:\d\d UTC  provider: (.*)\n`)

// splitView splits a monitor round into its provider text and the lines after the header.
func splitView(t *testing.T, out string) (provider, rest string) {
	t.Helper()
	m := monitorHeader.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no monitor header:\n%s", out)
	}
	return m[1], strings.TrimPrefix(out, m[0])
}

func TestE2EMonitorShowsAndChangesNothing(t *testing.T) {
	p := newPRE2E(t)
	p.merged(t)
	url := p.gh.pullURL(1)

	before := p.snapshot(t)
	writes := p.gh.writeCount()
	r := p.mustRalph(t, "monitor")
	provider, rest := splitView(t, r.stdout)
	want := "B-1  IN_PROGRESS  Ship the files\n" +
		"  S-1  IN_REVIEW  Deliver the files\n" +
		"       pull request #1 merged on the host (recorded: open) (draft) " + url + "\n" +
		"       next: record: pull request #1: merged on the host (recorded: open)\n" +
		"       next: close: delete branch " + loopBranch + "\n" +
		"  S-2  READY  Second slice  waiting on S-1\n" +
		"2" + footerPlural + "\n"
	if provider != "ok" || rest != want || r.stderr != "" {
		t.Errorf("provider %q, stderr %q\n got:\n%s\nwant:\n%s", provider, r.stderr, rest, want)
	}
	if after := p.snapshot(t); after != before {
		t.Errorf("monitor changed the project:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if n := p.gh.writeCount(); n != writes {
		t.Errorf("monitor made %d write requests", n-writes)
	}

	requests := p.gh.requestCount()
	r = p.mustRalph(t, "monitor", "--no-poll")
	provider, rest = splitView(t, r.stdout)
	if provider != "not polled" || strings.Contains(rest, "merged on the host") {
		t.Errorf("--no-poll: provider %q\n%s", provider, rest)
	}
	wantContains(t, "--no-poll view", rest, "       pull request #1 open "+url+"\n")
	if n := p.gh.requestCount(); n != requests {
		t.Errorf("--no-poll made %d requests", n-requests)
	}
	if after := p.snapshot(t); after != before {
		t.Error("--no-poll changed the project")
	}

	p.withoutToken()
	requests = p.gh.requestCount()
	r = p.mustRalph(t, "monitor")
	provider, rest = splitView(t, r.stdout)
	if provider != "ok" {
		t.Errorf("provider = %q: the token is read when the host is asked", provider)
	}
	wantContains(t, "view without a token", rest, "       pull request #1 open "+url+"\n", "       error: ", "GITHUB_TOKEN", "  S-2  READY  Second slice  waiting on S-1\n")
	if n := p.gh.requestCount(); n != requests {
		t.Errorf("a run without a token made %d requests", n-requests)
	}
	if after := p.snapshot(t); after != before {
		t.Error("the run without a token changed the project")
	}

	p.env = append(p.env, "RALPH_PR_PROVIDER=bitbucket")
	r = p.mustRalph(t, "monitor")
	provider, rest = splitView(t, r.stdout)
	if !strings.HasPrefix(provider, "unavailable (") || !strings.HasSuffix(provider, ")") {
		t.Errorf("provider = %q, want unavailable (...)", provider)
	}
	wantContains(t, "view without a provider", rest, "       pull request #1 open "+url+"\n", "  S-2  READY  Second slice  waiting on S-1\n")
	if after := p.snapshot(t); after != before {
		t.Error("the run without a provider changed the project")
	}
}

func TestE2EMonitorWatchStopsOnInterrupt(t *testing.T) {
	p := newPRE2E(t)
	p.merged(t)
	before := p.snapshot(t)

	w := p.watch(t, "monitor", "--watch", "--interval", "10s")
	w.waitFor(t, "ralph monitor  ", 2)
	if code := w.interrupt(t); code != 0 {
		t.Errorf("exit %d after SIGINT\nstdout: %s\nstderr: %s", code, w.stdout, w.err)
	}
	out := w.stdout.String()
	if n := strings.Count(out, "ralph monitor  "); n < 2 {
		t.Errorf("rounds = %d", n)
	}
	if strings.Count(out, "next: close: delete branch "+loopBranch) != strings.Count(out, "ralph monitor  ") {
		t.Errorf("each round should show the view:\n%s", out)
	}
	if w.err.String() != "" {
		t.Errorf("stderr = %q", w.err)
	}
	if after := p.snapshot(t); after != before {
		t.Error("watching changed the project")
	}
}

func TestE2EApplyRecordsMergeAndCloses(t *testing.T) {
	url := func(p *prE2E) string { return p.gh.pullURL(1) }

	t.Run("a bare apply records the merge and applies nothing else", func(t *testing.T) {
		p := newPRE2E(t)
		p.merged(t)
		p.onMain(t)
		r := p.mustRalph(t, "apply")
		want := "recorded S-1: Pull request " + url(p) + " was merged.\n" +
			"not applied: close: S-1: delete branch " + loopBranch + "; apply it with --close\n" +
			"not applied: continue: S-1: start the loop for S-2; apply it with --continue\n" +
			"apply: recorded 1, created 0 tasks, closed 0 slices\n" +
			applyHintLine + "\n"
		if !strings.HasSuffix(r.stdout, want) {
			t.Errorf("apply output:\n%s\nwant it to end with:\n%s", r.stdout, want)
		}
		if prs := p.pullRequests(t, "S-1"); len(prs) != 1 || prs[0]["state"] != "merged" {
			t.Errorf("pull requests = %v", prs)
		}
		if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  MERGED  ") {
			t.Errorf("slice status line = %q", line)
		}
		if !p.hasLocalBranch(loopBranch) {
			t.Error("a bare apply deleted the branch")
		}
		if n := p.noteCount(t, "S-1", "Merged: "); n != 0 {
			t.Errorf("a bare apply added %d close notes", n)
		}
		_, rest := splitView(t, p.mustRalph(t, "monitor").stdout)
		if strings.Contains(rest, "waiting on") || !strings.Contains(rest, "  S-2  READY  Second slice\n") {
			t.Errorf("S-2 still waits:\n%s", rest)
		}
		_, rest = splitView(t, p.mustRalph(t, "monitor", "S-1").stdout)
		wantContains(t, "monitor S-1", rest, "  S-1  MERGED  Deliver the files\n", "next: close: delete branch "+loopBranch+"\n", "next: continue: start the loop for S-2\n")
	})

	t.Run("close deletes the branch and updates the base", func(t *testing.T) {
		p := newPRE2E(t)
		p.merged(t)
		p.onMain(t)
		if !strings.Contains(p.repo.Git("-C", p.bare, "ls-tree", "--name-only", "main"), "feature.txt") {
			t.Fatal("the merge left no content on the base")
		}
		r := p.mustRalph(t, "apply", "--close")
		for _, want := range []string{
			"recorded S-1: Pull request " + url(p) + " was merged.\n",
			"S-1: deleted local branch " + loopBranch + "\n",
			"S-1: updated main from origin\n",
			"apply: recorded 1, created 0 tasks, closed 1 slices\n",
		} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("output lacks %q:\n%s", want, r.stdout)
			}
		}
		if strings.Contains(r.stdout, "nothing was applied") {
			t.Errorf("hint printed after a flagged run:\n%s", r.stdout)
		}
		if p.hasLocalBranch(loopBranch) {
			t.Error("the local branch is still there")
		}
		if !p.remoteHasBranch(loopBranch) {
			t.Error("the default setting deleted the remote branch")
		}
		for _, f := range []string{"feature.txt", "extra.txt"} {
			if _, err := os.Stat(filepath.Join(p.dir, f)); err != nil {
				t.Errorf("the merged content is not in the working tree: %v", err)
			}
		}
		if got, want := p.repo.Git("rev-parse", "main"), p.repo.Git("-C", p.bare, "rev-parse", "main"); got != want {
			t.Errorf("main = %s, origin has %s", got, want)
		}
		if n := p.noteCount(t, "S-1", "Merged: "); n != 1 {
			t.Errorf("close notes = %d, want 1: %v", n, p.typedNotes(t, "S-1"))
		}

		before := p.snapshot(t)
		r = p.mustRalph(t, "apply", "--close")
		if strings.Contains(r.stdout, "deleted") || strings.Contains(r.stdout, "updated main") {
			t.Errorf("a second close acted:\n%s", r.stdout)
		}
		wantContains(t, "second close", r.stdout, "apply: recorded 0, created 0 tasks, closed 0 slices\n")
		if n := p.noteCount(t, "S-1", "Merged: "); n != 1 {
			t.Errorf("close notes after a second close = %d", n)
		}
		if after := p.snapshot(t); after != before {
			t.Error("a second close changed the project")
		}
	})

	t.Run("both also deletes the remote branch", func(t *testing.T) {
		p := newPRE2E(t)
		p.projectSetting(t, settingBoth)
		p.merged(t)
		p.onMain(t)
		r := p.mustRalph(t, "apply", "--close")
		wantContains(t, "close", r.stdout, "S-1: deleted local branch "+loopBranch+"\n", "S-1: deleted remote branch origin/"+loopBranch+"\n")
		if p.hasLocalBranch(loopBranch) || p.remoteHasBranch(loopBranch) {
			t.Errorf("a branch is still there: local %v, remote %v", p.hasLocalBranch(loopBranch), p.remoteHasBranch(loopBranch))
		}
		if n := p.noteCount(t, "S-1", "Merged: "); n != 1 {
			t.Errorf("close notes = %d, want 1", n)
		}
	})

	t.Run("a commit after the merge keeps the branch", func(t *testing.T) {
		p := newPRE2E(t)
		p.projectSetting(t, settingBoth)
		p.merged(t)
		p.repo.WriteFile("after.txt", "after the merge\n")
		p.repo.Git("add", "after.txt")
		p.repo.Git("commit", "-m", "A commit after the merge")
		extra := p.repo.Git("rev-parse", "HEAD")
		p.onMain(t)
		r := p.mustRalph(t, "apply", "--close")
		wantContains(t, "close", r.stdout, loopBranch+" has commits that were not in the merged pull request; kept\n")
		if !p.hasLocalBranch(loopBranch) || p.repo.Git("rev-parse", loopBranch) != extra {
			t.Error("the branch with the extra commit was not kept")
		}
		if strings.Contains(r.stdout, "deleted local branch") {
			t.Errorf("deleted the local branch:\n%s", r.stdout)
		}
		if p.remoteHasBranch(loopBranch) {
			t.Error("the remote branch has the tip the pull request had and should be deleted")
		}
	})

	t.Run("the checked out branch is kept", func(t *testing.T) {
		p := newPRE2E(t)
		p.merged(t)
		if got := p.branch(); got != loopBranch {
			t.Fatalf("branch = %q", got)
		}
		r := p.mustRalph(t, "apply", "--close")
		wantContains(t, "close", r.stdout, loopBranch+" is checked out: git switch main first\n", "did not update main: the checkout is on "+loopBranch+"\n")
		if !p.hasLocalBranch(loopBranch) || p.branch() != loopBranch {
			t.Error("the checked out branch was touched")
		}
		if n := p.noteCount(t, "S-1", "Merged: "); n != 0 {
			t.Errorf("close notes = %d, want none", n)
		}
	})
}

func TestE2EApplyFeedbackFromSetting(t *testing.T) {
	feedback := func(p *prE2E) {
		p.gh.AddInlineComment(1, "alice", "Rename the value.", "feature.txt", 1)
		p.gh.AddReview(1, "bob", "Handle the error case.", "CHANGES_REQUESTED")
	}

	t.Run("the user file", func(t *testing.T) {
		p := newPRE2E(t)
		p.userConfig(t, userAuto("feedback"))
		p.open(t)
		feedback(p)
		r := p.mustRalph(t, "apply")
		wantContains(t, "apply", r.stdout, "created T-3, T-4 from review feedback on S-1\n", "apply: recorded 0, created 2 tasks, closed 0 slices\n")
		if strings.Contains(r.stdout, "nothing was applied") {
			t.Errorf("hint printed although a kind was applied:\n%s", r.stdout)
		}
		if r.stderr != "" {
			t.Errorf("stderr = %q", r.stderr)
		}
		if got := strings.Join(p.children(t, "S-1"), ","); got != "T-1 DONE,T-2 DONE,T-3 TODO,T-4 TODO" {
			t.Errorf("tasks = %s", got)
		}
		r = p.mustRalph(t, "apply")
		wantContains(t, "second apply", r.stdout, "apply: recorded 0, created 0 tasks, closed 0 slices\n")
		if len(p.children(t, "S-1")) != 4 {
			t.Errorf("a second apply created tasks: %v", p.children(t, "S-1"))
		}
	})

	t.Run("the project file is ignored", func(t *testing.T) {
		p := newPRE2E(t)
		p.projectSetting(t, userAuto("feedback"))
		p.open(t)
		feedback(p)
		r := p.mustRalph(t, "apply")
		wantContains(t, "stderr", r.stderr, `"follow_up.auto" is user-only and was ignored in the project file`)
		wantContains(t, "apply", r.stdout, "apply: recorded 0, created 0 tasks, closed 0 slices\n", applyHintLine+"\n")
		if got := strings.Join(p.children(t, "S-1"), ","); got != "T-1 DONE,T-2 DONE" {
			t.Errorf("tasks = %s: the project file's setting was used", got)
		}
	})
}

func TestE2EApplyClosedPullRequestNeedsYou(t *testing.T) {
	p := newPRE2E(t)
	p.open(t)
	url := p.gh.pullURL(1)
	p.gh.AddInlineComment(1, "alice", "Rename the value.", "feature.txt", 1)
	p.gh.Close(1)

	r := p.mustRalph(t, "apply")
	wantContains(t, "apply", r.stdout, "recorded S-1: Pull request "+url+" was closed without merging.\n",
		"needs you: pull request #1 was closed without merging; choose ralph pr reopen, rearticulate or cancel S-1\n")
	if prs := p.pullRequests(t, "S-1"); len(prs) != 1 || prs[0]["state"] != "closed" || prs[0]["resolution"] != "" {
		t.Errorf("pull requests = %v", prs)
	}
	if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  NEEDS_ATTENTION  ") {
		t.Errorf("slice status line = %q", line)
	}
	if line := p.firstLine(t, "status", "B-1"); !strings.HasPrefix(line, "B-1  NEEDS_ATTENTION  ") {
		t.Errorf("brief status line = %q", line)
	}

	before := p.snapshot(t)
	r = p.mustRalph(t, "apply", "--close", "--feedback")
	wantContains(t, "apply --close --feedback", r.stdout,
		"needs you: pull request #1 was closed without merging; choose ralph pr reopen, rearticulate or cancel S-1\n",
		"apply: recorded 0, created 0 tasks, closed 0 slices\n")
	if got := strings.Join(p.children(t, "S-1"), ","); got != "T-1 DONE,T-2 DONE" {
		t.Errorf("tasks = %s: feedback on a closed pull request became tasks", got)
	}
	if !p.hasLocalBranch(loopBranch) || !p.remoteHasBranch(loopBranch) {
		t.Error("a closed pull request lost a branch")
	}
	if after := p.snapshot(t); after != before {
		t.Errorf("apply changed the project:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	r = p.mustRalph(t, "monitor")
	provider, rest := splitView(t, r.stdout)
	if provider != "ok" {
		t.Errorf("provider = %q", provider)
	}
	wantContains(t, "monitor", rest, "  S-1  NEEDS_ATTENTION  Deliver the files\n",
		"       pull request #1 closed (draft) "+url+"\n",
		"       next: decision: pull request #1 was closed without merging; choose ralph pr reopen, rearticulate or cancel S-1\n")
}

// continueSetup merges S-1 with S-2 ready behind it, with close and continue
// applied by the user's settings, and leaves the checkout on main.
func continueSetup(t *testing.T) *prE2E {
	t.Helper()
	p := newPRE2E(t)
	p.userConfig(t, userAuto("close", "continue"))
	p.open(t)
	p.addSecond(t, false)
	p.q.squashMerge(t, 1)
	p.onMain(t)
	return p
}

func TestE2EApplyContinueStartsNextSlice(t *testing.T) {
	t.Run("records, closes and starts the loop once", func(t *testing.T) {
		p := continueSetup(t)
		r := p.mustRalph(t, "apply")
		out := r.stdout
		order := []string{
			"starting the loop for S-2\n",
			"  slice:    S-2  Second slice\n",
			"  T-3  attempt 1 of 3\n",
			"  S-2 done: 1 task, branch " + nextBranch + "\n",
			"recorded S-1: Pull request " + p.gh.pullURL(1) + " was merged.\n",
			"S-1: deleted local branch " + loopBranch + "\n",
			"S-1: updated main from origin\n",
			"apply: recorded 1, created 0 tasks, closed 1 slices\n",
		}
		last := -1
		for _, w := range order {
			at := strings.Index(out, w)
			if at < 0 || at < last {
				t.Errorf("output lacks %q in order:\n%s", w, out)
			}
			last = at
		}
		if got := p.branch(); got != nextBranch {
			t.Errorf("branch = %q, want %s", got, nextBranch)
		}
		if base := p.repo.Git("merge-base", "main", nextBranch); base != p.repo.Git("rev-parse", "main") {
			t.Errorf("%s does not start from the updated main", nextBranch)
		}
		if p.repo.Git("log", "-1", "--format=%s", "main") != "Squash merge of #1" {
			t.Errorf("main = %q", p.repo.Git("log", "-1", "--format=%s", "main"))
		}
		if got := p.repo.Git("log", "--format=%s", "main.."+nextBranch); got != "T-3: Write second" {
			t.Errorf("commits on %s = %q", nextBranch, got)
		}
		if links := p.linkTargets(t, "S-2"); !contains(links, "loop:auto-started") {
			t.Errorf("S-2 links = %v", links)
		}
		if got := p.status(t, "T-3"); got != "DONE" {
			t.Errorf("T-3 = %s", got)
		}
		calls := p.fakeCalls()

		p.onMain(t)
		r = p.mustRalph(t, "apply")
		if strings.Contains(r.stdout, "starting the loop") || p.fakeCalls() != calls {
			t.Errorf("a second apply started the loop again:\n%s", r.stdout)
		}
	})

	t.Run("a dirty tree refuses and starts nothing", func(t *testing.T) {
		p := continueSetup(t)
		calls := p.fakeCalls()
		p.repo.WriteFile("stray.txt", "x\n")
		r := p.mustRalph(t, "apply")
		wantContains(t, "apply", r.stdout, "not starting S-2: the working tree has changes: stray.txt")
		if strings.Contains(r.stdout, "starting the loop") || p.fakeCalls() != calls {
			t.Errorf("the loop started:\n%s", r.stdout)
		}
		if links := p.linkTargets(t, "S-2"); contains(links, "loop:auto-started") {
			t.Errorf("S-2 was marked: %v", links)
		}
		if p.hasLocalBranch(nextBranch) {
			t.Error("the next slice's branch was created")
		}
	})

	t.Run("a stalled loop exits 2", func(t *testing.T) {
		p := continueSetup(t)
		for i := 1; i <= 3; i++ {
			p.steer(t, fmt.Sprintf("nochange-%d", p.fakeCalls()+i), "")
		}
		r := p.run(t, "apply")
		wantExit(t, r, 2)
		wantContains(t, "apply", r.stdout, "starting the loop for S-2\n", "  S-2 stopped: ")
		if links := p.linkTargets(t, "S-2"); !contains(links, "loop:auto-started") {
			t.Errorf("S-2 links = %v", links)
		}
		r = p.mustRalph(t, "apply")
		if strings.Contains(r.stdout, "starting the loop") {
			t.Errorf("a stalled slice was started again:\n%s", r.stdout)
		}
	})
}

func TestE2EMonitorWatchApply(t *testing.T) {
	p := newPRE2E(t)
	p.userConfig(t, userAuto("close"))
	p.merged(t)
	p.onMain(t)
	url := p.gh.pullURL(1)

	w := p.watch(t, "monitor", "--watch", "--apply", "--interval", "10s")
	w.waitFor(t, "ralph monitor  ", 1)
	w.waitFor(t, "nothing to do\n", 1)
	if code := w.interrupt(t); code != 0 {
		t.Errorf("exit %d after SIGINT\nstdout: %s\nstderr: %s", code, w.stdout, w.err)
	}
	out := w.stdout.String()
	lines := "recorded S-1: Pull request " + url + " was merged.\n" +
		"S-1: deleted local branch " + loopBranch + "\n" +
		"S-1: updated main from origin\n" +
		"not applied: continue: S-1: start the loop for S-2; apply it with --continue\n"
	if !strings.HasPrefix(out, lines+"ralph monitor  ") {
		t.Errorf("the first round did not apply before the view:\n%s", out)
	}
	if strings.Contains(out[len(lines):], "deleted local branch") || strings.Contains(out[len(lines):], "recorded S-1") {
		t.Errorf("a later round closed again:\n%s", out)
	}
	if p.hasLocalBranch(loopBranch) {
		t.Error("the branch is still there")
	}
	if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  MERGED  ") {
		t.Errorf("slice status line = %q", line)
	}

	r := p.run(t, "monitor", "--apply")
	wantExit(t, r, 1)
	if r.stderr != "ralph: --apply needs --watch; use ralph apply for one run\n" || r.stdout != "" {
		t.Errorf("stdout = %q, stderr = %q", r.stdout, r.stderr)
	}
}

func TestE2EWholeMethod(t *testing.T) {
	p := newPRE2E(t)

	p.seedSlice(t)
	for _, name := range []string{"feature", "extra"} {
		made := e2eOK(t, p.cs, "unit_create", map[string]any{
			"level": "task", "parent_id": "S-1", "title": "Write " + name,
			"body":                "Create the file " + name + ".txt.\nFAKE-WRITE " + name + ".txt",
			"acceptance_criteria": name + ".txt exists",
		})
		e2eOK(t, p.cs, "task_set_state", map[string]any{"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready"})
	}
	wantContains(t, "loop", p.mustRalph(t, "loop", "S-1").stdout, "S-1 done")

	create := p.mustRalph(t, "pr", "create", "S-1")
	url := p.gh.pullURL(1)
	wantContains(t, "pr create", create.stdout, "pull request: "+url+" (draft)")

	p.gh.AddInlineComment(1, "alice", "Rename the value.\nFAKE-WRITE renamed.txt", "feature.txt", 1)
	wantContains(t, "feedback", p.mustRalph(t, "pr", "feedback", "S-1", "--create").stdout, "created T-3")
	wantContains(t, "second loop", p.mustRalph(t, "loop", "S-1").stdout, "S-1 done")
	if got := p.status(t, "T-3"); got != "DONE" {
		t.Errorf("T-3 = %s", got)
	}
	wantContains(t, "second create", p.mustRalph(t, "pr", "create", "S-1").stdout, "pull request: "+url+" (reused)")
	if n := len(p.gh.creates()); n != 1 {
		t.Errorf("pull requests created = %d, want 1", n)
	}
	if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  IN_REVIEW  ") {
		t.Errorf("slice status line = %q", line)
	}

	p.q.squashMerge(t, 1)
	p.onMain(t)
	r := p.mustRalph(t, "apply", "--close")
	wantContains(t, "apply", r.stdout, "recorded S-1: Pull request "+url+" was merged.\n",
		"S-1: deleted local branch "+loopBranch+"\n", "S-1: updated main from origin\n",
		"apply: recorded 1, created 0 tasks, closed 1 slices\n")
	if p.hasLocalBranch(loopBranch) {
		t.Error("the branch is still there")
	}
	for _, f := range []string{"feature.txt", "extra.txt", "renamed.txt"} {
		if _, err := os.Stat(filepath.Join(p.dir, f)); err != nil {
			t.Errorf("the merged content lacks %s: %v", f, err)
		}
	}

	if line := p.firstLine(t, "status", "S-1"); !strings.HasPrefix(line, "S-1  MERGED  ") {
		t.Errorf("slice status line = %q", line)
	}
	if line := p.firstLine(t, "status", "B-1"); !strings.HasPrefix(line, "B-1  DONE  ") {
		t.Errorf("brief status line = %q", line)
	}
	all := p.mustRalph(t, "status", "--all").stdout
	wantContains(t, "status --all", all, "MERGED", "DONE")
	if !p.hasDatabase(t) {
		t.Error("no database")
	}
	_, rest := splitView(t, p.mustRalph(t, "monitor", "--all").stdout)
	wantContains(t, "monitor --all", rest, "S-1  MERGED  Deliver the files", "nothing to do\n")
}

func (p *prE2E) remoteHasBranch(branch string) bool { return p.q.remoteHasBranch(branch) }
