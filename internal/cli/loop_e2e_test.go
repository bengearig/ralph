package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// loopFakeClaude stands in for claude: it records its arguments and standard
// input, writes the files the prompt asks for with FAKE-WRITE lines, and prints
// a stream-json run. The test steers it with files next to the script: nochange-N
// skips the edits of call N, stream and stream-N replace the output (@TOKEN@ is
// replaced by $GITHUB_TOKEN), hang makes it sleep with a child, exit sets the
// exit status.
const loopFakeClaude = `#!/bin/sh
dir=$(dirname "$0")
if [ "$1" = "--version" ]; then echo "2.1.325 (Claude Code)"; exit 0; fi
n=$(( $(cat "$dir/count" 2>/dev/null || echo 0) + 1 ))
echo $n > "$dir/count"
printf '%s\n' "$@" > "$dir/args-$n"
cat > "$dir/stdin-$n"
if [ ! -e "$dir/nochange-$n" ]; then
  grep '^FAKE-WRITE ' "$dir/stdin-$n" | while read -r _ path; do
    mkdir -p "$(dirname "$path")"
    echo "written by call $n" > "$path"
  done
fi
if [ -e "$dir/hang" ]; then
  echo partial > partial.txt
  sleep 300 &
  echo $! > "$dir/child-pid"
  echo $$ > "$dir/pid"
  wait
fi
if [ -e "$dir/stream-$n" ]; then
  sed "s|@TOKEN@|$GITHUB_TOKEN|g" "$dir/stream-$n"
elif [ -e "$dir/stream" ]; then
  sed "s|@TOKEN@|$GITHUB_TOKEN|g" "$dir/stream"
else
  echo '{"type":"system","subtype":"init","session_id":"sess"}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Working on it."}]}}'
  echo '{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.01,"result":"All done.","session_id":"sess"}'
fi
if [ -e "$dir/exit" ]; then exit "$(cat "$dir/exit")"; fi
exit 0
`

const loopBranch = "ralph/s-1-deliver-the-files"

var loopFiles = []string{"one", "two", "three"}

type loopE2E struct {
	*e2eEnv
	fake string
	cs   *mcp.ClientSession
}

// newLoopE2E builds an initialized, committed repository with a fake claude on
// the path and the verification command and failure limit set (0 keeps the default).
func newLoopE2E(t *testing.T, verifyCommand string, maxFailures int) *loopE2E {
	t.Helper()
	e := newE2EEnv(t)
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "claude"), []byte(loopFakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, kv := range e.env {
		if strings.HasPrefix(kv, "PATH=") {
			e.env[i] = "PATH=" + fake + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
	}
	e.env = append(e.env, "RALPH_E2E_FLAG="+filepath.Join(t.TempDir(), "flag"))
	l := &loopE2E{e2eEnv: e, fake: fake}

	l.mustRalph(t, "init", "--harness", "claude-code")
	cfg := "loop:\n  harness: claude-code\n"
	if maxFailures > 0 {
		cfg += fmt.Sprintf("  max_failures: %d\n", maxFailures)
	}
	cfg += "verify:\n  commands:\n    - '" + verifyCommand + "'\n"
	path := filepath.Join(e.dir, ".ralph", "project.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, []byte(cfg)...), 0o644); err != nil {
		t.Fatal(err)
	}
	e.repo.Git("add", "-A")
	e.repo.Git("commit", "-m", "Install ralph")
	l.cs = e.connect(t, &bytes.Buffer{})
	return l
}

func (l *loopE2E) mustRalph(t *testing.T, args ...string) e2eRun {
	t.Helper()
	r := l.run(t, args...)
	if r.code != 0 {
		t.Fatalf("ralph %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func (l *loopE2E) seedSlice(t *testing.T) {
	t.Helper()
	e2eOK(t, l.cs, "unit_create", map[string]any{"level": "brief", "title": "Ship the files", "body": "Ship the files."})
	e2eOK(t, l.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Deliver the files", "body": "Deliver them one by one."})
}

// addTask creates the task for loopFiles[i]; its body asks the fake for that file.
func (l *loopE2E) addTask(t *testing.T, i int, promote bool) {
	t.Helper()
	name := loopFiles[i]
	made := e2eOK(t, l.cs, "unit_create", map[string]any{
		"level": "task", "parent_id": "S-1", "title": "Write " + name,
		"body":                "Create the file " + name + ".txt.\nFAKE-WRITE " + name + ".txt",
		"acceptance_criteria": name + ".txt exists",
	})
	if promote {
		e2eOK(t, l.cs, "task_set_state", map[string]any{
			"id": made["id"], "fingerprint": made["fingerprint"], "state": "TODO", "reason": "ready",
		})
	}
}

func (l *loopE2E) seed(t *testing.T, tasks int) {
	t.Helper()
	l.seedSlice(t)
	for i := 0; i < tasks; i++ {
		l.addTask(t, i, true)
	}
}

func (l *loopE2E) setState(t *testing.T, id, state, reason string) {
	t.Helper()
	u := e2eOK(t, l.cs, "unit_get", map[string]any{"id": id})
	e2eOK(t, l.cs, "task_set_state", map[string]any{"id": id, "fingerprint": u["fingerprint"], "state": state, "reason": reason})
}

func (l *loopE2E) status(t *testing.T, id string) string {
	t.Helper()
	s, _ := e2eOK(t, l.cs, "unit_get", map[string]any{"id": id})["status"].(string)
	return s
}

func (l *loopE2E) notes(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	list, _ := e2eOK(t, l.cs, "unit_get", map[string]any{"id": id})["notes"].([]any)
	for _, n := range list {
		out = append(out, n.(map[string]any)["body"].(string))
	}
	return out
}

func (l *loopE2E) attemptNotes(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	for _, n := range l.notes(t, id) {
		if strings.HasPrefix(n, "Attempt ") {
			out = append(out, n)
		}
	}
	return out
}

func (l *loopE2E) commitLinks(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	list, _ := e2eOK(t, l.cs, "unit_get", map[string]any{"id": id})["links"].([]any)
	for _, item := range list {
		if target, _ := item.(map[string]any)["target"].(string); strings.HasPrefix(target, "commit:") {
			out = append(out, strings.TrimPrefix(target, "commit:"))
		}
	}
	return out
}

func (l *loopE2E) fakeFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(l.fake, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (l *loopE2E) fakeCalls() int {
	b, err := os.ReadFile(filepath.Join(l.fake, "count"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func (l *loopE2E) steer(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.fake, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (l *loopE2E) branch() string { return l.repo.Git("rev-parse", "--abbrev-ref", "HEAD") }

func (l *loopE2E) recordDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(l.dir, ".ralph", "runs", "S-1", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	return dirs
}

func (l *loopE2E) readRecord(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func wantExit(t *testing.T, r e2eRun, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.stdout, r.stderr)
	}
}

func wantContains(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

type bgLoop struct {
	cmd          *exec.Cmd
	stdout, serr *bytes.Buffer
	done         chan struct{}
	err          error
}

// start runs ralph in the background.
func (l *loopE2E) start(t *testing.T, args ...string) *bgLoop {
	t.Helper()
	b := &bgLoop{stdout: &bytes.Buffer{}, serr: &bytes.Buffer{}, done: make(chan struct{})}
	b.cmd = l.command(context.Background(), args...)
	b.cmd.Stdout, b.cmd.Stderr = b.stdout, b.serr
	if err := b.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		b.err = b.cmd.Wait()
		close(b.done)
	}()
	t.Cleanup(func() {
		select {
		case <-b.done:
		default:
			_ = b.cmd.Process.Kill()
			<-b.done
		}
		for _, name := range []string{"pid", "child-pid"} {
			if pid := l.pidOf(name); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return b
}

func (l *loopE2E) pidOf(name string) int {
	b, err := os.ReadFile(filepath.Join(l.fake, name))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func (b *bgLoop) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-b.done:
	case <-time.After(60 * time.Second):
		t.Fatal("the loop did not end")
	}
	var exit *exec.ExitError
	if b.err == nil {
		return 0
	}
	if e, ok := b.err.(*exec.ExitError); ok {
		exit = e
		return exit.ExitCode()
	}
	t.Fatal(b.err)
	return -1
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// alive reports whether the process runs; a zombie counts as ended.
func alive(pid int) bool {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		s := string(b)
		if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) {
			return s[i+2] != 'Z'
		}
	}
	return syscall.Kill(pid, 0) == nil
}

func waitDead(t *testing.T, what string, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("%s (pid %d) is still running", what, pid)
}

func TestE2ELoopDeliversASlice(t *testing.T) {
	l := newLoopE2E(t, "test -f one.txt", 0)
	l.seed(t, 3)
	e2eOK(t, l.cs, "doc_create", map[string]any{"kind": "reference", "title": "House style", "body": "DOCUMENT-TEXT-MARKER keep files tiny."})
	e2eOK(t, l.cs, "link_add", map[string]any{"id": "S-1", "kind": "document", "target": "D-1"})

	head := l.repo.Git("rev-parse", "HEAD")
	lock := filepath.Join(l.dir, ".ralph", "loop.lock")
	_, lockBefore := os.Stat(lock)

	dry := l.run(t, "loop", "S-1", "--dry-run")
	wantExit(t, dry, 0)
	wantContains(t, "dry run output", dry.stdout, "slice:    S-1", "creating from main", "verify:   test -f one.txt",
		"T-1  TODO  Write one", "T-2  TODO  Write two", "T-3  TODO  Write three")
	if got := l.branch(); got != "main" {
		t.Errorf("branch after dry run = %q", got)
	}
	if got := l.repo.Git("rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved on a dry run")
	}
	if got := l.repo.Git("status", "--short"); got != "" {
		t.Errorf("status after dry run = %q", got)
	}
	if got := l.repo.Git("branch", "--list", "ralph/*"); got != "" {
		t.Errorf("a dry run created branches: %q", got)
	}
	for _, id := range []string{"T-1", "T-2", "T-3"} {
		if s := l.status(t, id); s != "TODO" {
			t.Errorf("%s after dry run = %s", id, s)
		}
	}
	if _, lockAfter := os.Stat(lock); (lockBefore == nil) != (lockAfter == nil) {
		t.Errorf("the lock changed on a dry run: before %v, after %v", lockBefore, lockAfter)
	}
	if l.fakeCalls() != 0 || len(l.recordDirs(t)) != 0 {
		t.Error("a dry run ran the harness or wrote records")
	}

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 0)
	wantContains(t, "loop output", r.stdout, "S-1 done: 3 tasks, branch "+loopBranch)
	if got := l.branch(); got != loopBranch {
		t.Fatalf("branch = %q, want %s", got, loopBranch)
	}

	hashes := strings.Fields(l.repo.Git("log", "--reverse", "--format=%H", "main..HEAD"))
	if len(hashes) != 3 {
		t.Fatalf("commits on the branch = %v", hashes)
	}
	for i, h := range hashes {
		id, name := fmt.Sprintf("T-%d", i+1), loopFiles[i]
		want := fmt.Sprintf("%s: Write %s\n\nRalph-Task: %s", id, name, id)
		if got := l.repo.Git("log", "-1", "--format=%B", h); got != want {
			t.Errorf("commit %d message = %q, want %q", i+1, got, want)
		}
		if files := l.repo.Git("show", "--format=", "--name-only", h); files != name+".txt" {
			t.Errorf("commit %d files = %q", i+1, files)
		}
		if s := l.status(t, id); s != "DONE" {
			t.Errorf("%s = %s, want DONE", id, s)
		}
		if links := l.commitLinks(t, id); len(links) != 1 || links[0] != h {
			t.Errorf("%s commit links = %v, want [%s]", id, links, h)
		}
		if notes := l.attemptNotes(t, id); len(notes) != 1 || !strings.Contains(notes[0], "passed: commit "+h[:12]) {
			t.Errorf("%s attempt notes = %v", id, notes)
		}
	}
	if s := l.status(t, "S-1"); s != "DONE" {
		t.Errorf("slice = %s, want DONE", s)
	}
	if got := l.repo.Git("status", "--short"); got != "" {
		t.Errorf("status after the loop = %q", got)
	}

	dirs := l.recordDirs(t)
	if len(dirs) != 3 {
		t.Fatalf("record directories = %v", dirs)
	}
	for i, dir := range dirs {
		var names []string
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if strings.Join(names, " ") != "output.jsonl prompt.md record.json verify.txt" {
			t.Errorf("%s holds %v", filepath.Base(dir), names)
		}
		rec := l.readRecord(t, dir)
		if rec["task"] != fmt.Sprintf("T-%d", i+1) || rec["outcome"] != "done" || rec["commit"] != hashes[i] {
			t.Errorf("record %d = %v", i+1, rec)
		}
	}

	if got := l.fakeCalls(); got != 3 {
		t.Fatalf("the fake ran %d times, want 3", got)
	}
	for i := 1; i <= 3; i++ {
		name := loopFiles[i-1]
		stdin, args := l.fakeFile(t, fmt.Sprintf("stdin-%d", i)), l.fakeFile(t, fmt.Sprintf("args-%d", i))
		wantContains(t, "prompt on stdin", stdin, fmt.Sprintf("# Task T-%d: Write %s", i, name), "FAKE-WRITE "+name+".txt",
			name+".txt exists", "DOCUMENT-TEXT-MARKER", "test -f one.txt")
		if strings.Contains(args, "FAKE-WRITE") || strings.Contains(args, "# Task") || strings.Contains(args, "DOCUMENT-TEXT-MARKER") {
			t.Errorf("the prompt reached the arguments of call %d:\n%s", i, args)
		}
		lines := strings.Split(strings.TrimSpace(args), "\n")
		wantContains(t, "arguments", strings.Join(lines, "\n"), "-p", "--output-format\nstream-json", "--strict-mcp-config",
			"--setting-sources\nuser,local", "--permission-mode\ndontAsk", "--permission-prompts\nnone", "--no-session-persistence")
		if !strings.Contains(args, "Edit(./**)") {
			t.Errorf("the restricted settings are missing from the arguments:\n%s", args)
		}
	}

	again := l.run(t, "loop", "S-1")
	wantExit(t, again, 0)
	wantContains(t, "second run output", again.stdout, "S-1 has nothing left to do")
	if l.fakeCalls() != 3 {
		t.Error("the second run called the harness")
	}
}

func TestE2ELoopRetriesThenSucceeds(t *testing.T) {
	l := newLoopE2E(t, "echo ok", 0)
	l.seed(t, 1)
	l.steer(t, "nochange-1", "")

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 0)
	wantContains(t, "output", r.stdout, "T-1  attempt 1 of 3", "attempt 1 of 3 failed: the agent made no changes",
		"T-1  attempt 2 of 3", "T-1  done", "S-1 done: 1 task")
	if strings.Contains(r.stdout, "attempt 3 of 3") {
		t.Errorf("a third attempt ran:\n%s", r.stdout)
	}
	if got := l.fakeCalls(); got != 2 {
		t.Errorf("the fake ran %d times, want 2", got)
	}
	if first := l.fakeFile(t, "stdin-1"); strings.Contains(first, "The previous attempt failed") {
		t.Errorf("the first prompt has a retry section:\n%s", first)
	}
	wantContains(t, "second prompt", l.fakeFile(t, "stdin-2"), "## The previous attempt failed (attempt 1 of 3)", "the agent made no changes")
	if s := l.status(t, "T-1"); s != "DONE" {
		t.Errorf("T-1 = %s", s)
	}
	notes := l.attemptNotes(t, "T-1")
	if len(notes) != 2 {
		t.Fatalf("attempt notes = %v", notes)
	}
	wantContains(t, "first note", notes[0], "Attempt 1 of 3 failed: the agent made no changes")
	wantContains(t, "second note", notes[1], "Attempt 2 of 3 passed: commit")
	if n := len(l.repo.Git("log", "--format=%H", "main..HEAD")); n == 0 || len(strings.Fields(l.repo.Git("log", "--format=%H", "main..HEAD"))) != 1 {
		t.Errorf("want exactly one commit on the branch")
	}
	if dirs := l.recordDirs(t); len(dirs) != 2 {
		t.Errorf("record directories = %v", dirs)
	}
}

func TestE2ELoopStallBlocksTask(t *testing.T) {
	l := newLoopE2E(t, `test -f "$RALPH_E2E_FLAG"`, 0)
	l.seed(t, 2)

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 2)
	const reason = `stalled after 3 failed attempts: verification failed: test -f "$RALPH_E2E_FLAG" (exit 1)`
	wantContains(t, "output", r.stdout, "T-1  attempt 3 of 3 failed: verification failed", "T-1  stalled: "+reason,
		"S-1 stopped: T-1 "+reason,
		"the tree was left as the agent left it; look at the run records under .ralph/runs/S-1/",
		"fix or revert it, unblock T-1 with a reason, and run the loop again")
	if got := l.fakeCalls(); got != 3 {
		t.Errorf("the fake ran %d times, want 3", got)
	}
	if s := l.status(t, "T-1"); s != "BLOCKED" {
		t.Errorf("T-1 = %s, want BLOCKED", s)
	}
	if !strings.Contains(strings.Join(l.notes(t, "T-1"), "\n"), reason) {
		t.Errorf("the block reason is not in the notes: %v", l.notes(t, "T-1"))
	}
	if s := l.status(t, "T-2"); s != "TODO" {
		t.Errorf("T-2 = %s, want TODO", s)
	}
	if len(l.attemptNotes(t, "T-1")) != 3 {
		t.Errorf("attempt notes = %v", l.attemptNotes(t, "T-1"))
	}
	if got := l.repo.Git("status", "--short"); got != "?? one.txt" {
		t.Errorf("tree after the stall = %q, want the last attempt's file", got)
	}
	if n := strings.TrimSpace(l.repo.Git("log", "--format=%H", "main..HEAD")); n != "" {
		t.Errorf("a stalled task left commits: %q", n)
	}

	refused := l.run(t, "loop", "S-1")
	wantExit(t, refused, 1)
	wantContains(t, "refusal", refused.stderr, "the working tree has changes: one.txt")
	if l.fakeCalls() != 3 {
		t.Error("the refused loop ran the harness")
	}

	l.repo.Git("clean", "-fd")
	l.setState(t, "T-1", "TODO", "verification fixed")
	if err := os.WriteFile(l.flag(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := l.run(t, "loop", "S-1")
	wantExit(t, again, 0)
	wantContains(t, "second run output", again.stdout, "T-1  attempt 1 of 3", "T-1  done", "T-2  done", "S-1 done: 2 tasks")
	if strings.Contains(again.stdout, "attempt 2 of 3") {
		t.Errorf("the count did not start from zero:\n%s", again.stdout)
	}
	if s := l.status(t, "S-1"); s != "DONE" {
		t.Errorf("slice = %s", s)
	}
}

func (l *loopE2E) flag() string {
	for _, kv := range l.env {
		if v, ok := strings.CutPrefix(kv, "RALPH_E2E_FLAG="); ok {
			return v
		}
	}
	return ""
}

func TestE2ELoopDeniedActionFailsAttempt(t *testing.T) {
	l := newLoopE2E(t, "test -f never.txt", 1)
	l.seed(t, 1)
	denied, err := os.ReadFile(filepath.Join("..", "harness", "claudecode", "testdata", "denied.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	l.steer(t, "stream", string(denied))

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 2)
	wantContains(t, "output", r.stdout, "T-1  attempt 1 of 1 failed: denied actions (1): Bash echo hello > shell.txt")
	if strings.Contains(r.stdout, "verification failed") {
		t.Errorf("verification ran for a denied attempt:\n%s", r.stdout)
	}
	const suggestion = "to allow this command, add"
	notes := l.attemptNotes(t, "T-1")
	if len(notes) != 1 {
		t.Fatalf("attempt notes = %v", notes)
	}
	wantContains(t, "note", notes[0], "Attempt 1 of 1 failed: denied actions (1): Bash echo hello > shell.txt", suggestion, "headless.allow")

	dirs := l.recordDirs(t)
	if len(dirs) != 1 {
		t.Fatalf("record directories = %v", dirs)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "verify.txt")); err == nil {
		t.Error("a denied attempt wrote verify.txt")
	}
	rec := l.readRecord(t, dirs[0])
	denials, _ := rec["denials"].([]any)
	if len(denials) != 1 {
		t.Fatalf("denials = %v", rec["denials"])
	}
	d := denials[0].(map[string]any)
	if d["tool"] != "Bash" || d["target"] != "echo hello > shell.txt" {
		t.Errorf("denial = %v", d)
	}
	if s, _ := d["suggestion"].(string); !strings.Contains(s, "headless.allow") || !strings.Contains(s, "echo hello") {
		t.Errorf("suggestion = %q", s)
	}
	if rec["outcome"] != "failed" {
		t.Errorf("outcome = %v", rec["outcome"])
	}
}

func TestE2ELoopRefusals(t *testing.T) {
	t.Run("unrelated branch", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seed(t, 1)
		l.repo.Git("switch", "-c", "elsewhere")
		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, `the current branch is "elsewhere"`,
			"run git switch main to start the slice, or git switch "+loopBranch+" to continue it")
		if l.fakeCalls() != 0 {
			t.Error("the harness ran")
		}
	})

	t.Run("dirty tree", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seed(t, 1)
		l.repo.WriteFile("stray.txt", "x\n")
		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "the working tree has changes: stray.txt")
		if got := l.branch(); got != "main" {
			t.Errorf("branch = %q, the refused loop must not create one", got)
		}
	})

	t.Run("waiting slice", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seed(t, 1)
		e2eOK(t, l.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Later work"})
		e2eOK(t, l.cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
		r := l.run(t, "loop", "S-2")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "S-2 waits for S-1 (Deliver the files)")
	})

	t.Run("another loop holds the lock", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seed(t, 1)
		l.steer(t, "hang", "")
		first := l.start(t, "loop", "S-1")
		waitForFile(t, filepath.Join(l.fake, "pid"))

		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "another loop is running in this project", ".ralph/loop.lock")

		if err := first.cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		if code := first.wait(t); code != exitInterrupted {
			t.Errorf("the first loop exited %d, want %d", code, exitInterrupted)
		}
	})

	t.Run("task not ready", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seedSlice(t)
		l.addTask(t, 0, false)
		l.addTask(t, 1, true)
		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 2)
		wantContains(t, "output", r.stdout, "S-1 stopped: T-1 is PROPOSED: it must be articulated and made TODO before the loop can run it")
		if l.fakeCalls() != 0 {
			t.Error("the loop ran past a task that is not ready")
		}
		if s := l.status(t, "T-2"); s != "TODO" {
			t.Errorf("T-2 = %s, want TODO", s)
		}
	})

	t.Run("not a slice", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		e2eOK(t, l.cs, "unit_create", map[string]any{"level": "task", "title": "Orphan", "acceptance_criteria": "done"})
		e2eOK(t, l.cs, "unit_create", map[string]any{"level": "brief", "title": "A brief", "body": "Words."})
		r := l.run(t, "loop", "T-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "T-1 is a task, not a slice", "ralph quick start")
		r = l.run(t, "loop", "B-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "B-1 is a brief, not a slice")
		if r := l.run(t, "loop", "S-9"); r.code == 0 {
			t.Errorf("an unknown slice was accepted: %s", r.stdout)
		}
	})

	t.Run("runs not ignored", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		l.seed(t, 1)
		path := filepath.Join(l.dir, ".ralph", ".gitignore")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(b), "/runs/\n", "")), 0o644); err != nil {
			t.Fatal(err)
		}
		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, "git does not ignore .ralph/runs/", `run "ralph init"`)
		if l.fakeCalls() != 0 {
			t.Error("the harness ran")
		}
	})

	t.Run("outside a repository", func(t *testing.T) {
		l := newLoopE2E(t, "echo ok", 0)
		cmd := l.command(context.Background(), "loop", "S-1")
		cmd.Dir = t.TempDir()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err == nil || !asExit(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("err = %v", err)
		}
		wantContains(t, "stderr", stderr.String(), errNotInRepository.Error())
	})

	t.Run("before init", func(t *testing.T) {
		e := newE2EEnv(t)
		r := e.run(t, "loop", "S-1")
		wantExit(t, r, 1)
		wantContains(t, "stderr", r.stderr, errNoDatabase)
	})
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func TestE2ELoopRecoversInterruptedTask(t *testing.T) {
	t.Run("with its commit", func(t *testing.T) {
		l := newLoopE2E(t, "test -f one.txt", 0)
		l.seed(t, 2)
		l.setState(t, "T-1", "IN_PROGRESS", "interrupted")
		l.repo.Git("switch", "-c", loopBranch)
		l.repo.WriteFile("one.txt", "done before\n")
		l.repo.Git("add", "-A")
		l.repo.Git("commit", "-m", "T-1: Write one", "-m", "Ralph-Task: T-1")
		hash := l.repo.Git("rev-parse", "HEAD")

		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 0)
		if s := l.status(t, "T-1"); s != "DONE" {
			t.Errorf("T-1 = %s, want DONE", s)
		}
		if links := l.commitLinks(t, "T-1"); len(links) != 1 || links[0] != hash {
			t.Errorf("T-1 commit links = %v, want [%s]", links, hash)
		}
		if got := l.fakeCalls(); got != 1 {
			t.Fatalf("the fake ran %d times, want 1 (T-2 only)", got)
		}
		wantContains(t, "prompt", l.fakeFile(t, "stdin-1"), "# Task T-2: Write two")
		if strings.Contains(r.stdout, "T-1  attempt") {
			t.Errorf("T-1 was run again:\n%s", r.stdout)
		}
		if s := l.status(t, "S-1"); s != "DONE" {
			t.Errorf("slice = %s", s)
		}
	})

	t.Run("without its commit", func(t *testing.T) {
		l := newLoopE2E(t, "test -f one.txt", 0)
		l.seed(t, 2)
		l.setState(t, "T-1", "IN_PROGRESS", "interrupted")

		r := l.run(t, "loop", "S-1")
		wantExit(t, r, 0)
		wantContains(t, "output", r.stdout, "T-1  attempt 1 of 3", "T-1  done", "T-2  done")
		if got := l.fakeCalls(); got != 2 {
			t.Errorf("the fake ran %d times, want 2", got)
		}
		wantContains(t, "T-1 notes", strings.Join(l.notes(t, "T-1"), "\n"), "loop restarted; the interrupted attempt left no commit")
		if s := l.status(t, "T-1"); s != "DONE" {
			t.Errorf("T-1 = %s", s)
		}
	})
}

func TestE2ELoopInterruptStopsHarnessGroup(t *testing.T) {
	l := newLoopE2E(t, "echo ok", 0)
	l.seed(t, 2)
	l.steer(t, "hang", "")

	run := l.start(t, "loop", "S-1")
	waitForFile(t, filepath.Join(l.fake, "pid"))
	pid, child := l.pidOf("pid"), l.pidOf("child-pid")
	if pid == 0 || child == 0 || !alive(pid) || !alive(child) {
		t.Fatalf("the fake and its child must be running: %d, %d", pid, child)
	}
	if err := run.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := run.wait(t); code != exitInterrupted {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, exitInterrupted, run.stdout, run.serr)
	}
	waitDead(t, "the fake harness", pid)
	waitDead(t, "its child", child)

	wantContains(t, "output", run.stdout.String(), "S-1 interrupted")
	if s := l.status(t, "T-1"); s != "TODO" {
		t.Errorf("T-1 = %s, want TODO", s)
	}
	wantContains(t, "T-1 notes", strings.Join(l.notes(t, "T-1"), "\n"), "loop interrupted")
	if s := l.status(t, "T-2"); s != "TODO" {
		t.Errorf("T-2 = %s, want TODO", s)
	}
	if got := l.repo.Git("status", "--short"); !strings.Contains(got, "partial.txt") {
		t.Errorf("tree after the interrupt = %q, want the partial file", got)
	}
	if l.fakeCalls() != 1 {
		t.Errorf("the fake ran %d times, want 1", l.fakeCalls())
	}
}

func TestE2ELoopSecretsRedactedInRecords(t *testing.T) {
	const token = "ghp_ABCDEFGH12345678"
	if len(token) != 20 {
		t.Fatal("the test token must have 20 characters")
	}
	l := newLoopE2E(t, `echo "verifying with $GITHUB_TOKEN"`, 2)
	l.seed(t, 2)
	l.env = append(l.env, "GITHUB_TOKEN="+token)
	failing := `{"type":"system","subtype":"init","session_id":"sess"}
{"type":"assistant","message":{"content":[{"type":"text","text":"my token is @TOKEN@"}]}}
{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["rejected @TOKEN@"],"session_id":"sess"}
`
	for _, n := range []string{"1", "3", "4"} {
		l.steer(t, "stream-"+n, failing)
	}

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 2)
	if got := l.fakeCalls(); got != 4 {
		t.Fatalf("the fake ran %d times, want 4", got)
	}
	if s := l.status(t, "T-1"); s != "DONE" {
		t.Errorf("T-1 = %s, want DONE", s)
	}
	if s := l.status(t, "T-2"); s != "BLOCKED" {
		t.Errorf("T-2 = %s, want BLOCKED", s)
	}

	redactions := 0
	err := filepath.WalkDir(filepath.Join(l.dir, ".ralph", "runs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(token)) {
			t.Errorf("%s holds the secret", path)
		}
		redactions += strings.Count(string(b), "[redacted]")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if redactions == 0 {
		t.Error("no [redacted] marker was written")
	}
	for _, id := range []string{"T-1", "T-2"} {
		for _, n := range l.notes(t, id) {
			if strings.Contains(n, token) {
				t.Errorf("a note of %s holds the secret: %s", id, n)
			}
		}
	}
	if !strings.Contains(l.fakeFile(t, "stdin-2"), "rejected "+token) {
		t.Error("the harness itself should still see the retry text")
	}
}

func TestE2ELoopTrimsAndPurges(t *testing.T) {
	l := newLoopE2E(t, "echo ok", 0)
	l.seed(t, 1)
	wantExit(t, l.run(t, "loop", "S-1"), 0)
	real := l.recordDirs(t)
	if len(real) != 1 {
		t.Fatalf("record directories = %v", real)
	}
	runs := filepath.Join(l.dir, ".ralph", "runs", "S-1")

	copyAged := func(name string, age time.Duration) string {
		dst := filepath.Join(runs, name)
		if err := os.Mkdir(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(real[0])
		if err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(real[0], e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if e.Name() == "record.json" {
				var m map[string]any
				if err := json.Unmarshal(b, &m); err != nil {
					t.Fatal(err)
				}
				m["started_at"] = when.UTC().Format(time.RFC3339)
				if b, err = json.MarshalIndent(m, "", "  "); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dst, e.Name())
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, when, when); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(dst, when, when); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	day := 24 * time.Hour
	oldA := copyAged("20200101-000000-001-T-1", 40*day)
	oldB := copyAged("20200102-000000-001-T-1", 90*day)
	recent := copyAged("20260101-000000-001-T-1", 2*day)
	broken := filepath.Join(runs, "broken")
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "record.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * day)
	for _, p := range []string{filepath.Join(broken, "record.json"), broken} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	r := l.run(t, "loop", "S-1")
	wantExit(t, r, 0)
	wantContains(t, "output", r.stdout, "info: removed 2 run records older than 30 days", "S-1 has nothing left to do")
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if exists(oldA) || exists(oldB) {
		t.Error("records older than 30 days were kept")
	}
	if !exists(recent) || !exists(real[0]) || !exists(broken) {
		t.Error("a record that should stay was removed")
	}

	dry := l.run(t, "runs", "purge", "--older-than", "1d")
	wantExit(t, dry, 0)
	wantContains(t, "dry run output", dry.stdout, ".ralph/runs/S-1/20260101-000000-001-T-1", "1 records", "run again with --yes to remove them")
	if strings.Contains(dry.stdout, filepath.Base(real[0])) || strings.Contains(dry.stdout, "broken") {
		t.Errorf("the dry run lists records it must not:\n%s", dry.stdout)
	}
	if !exists(recent) {
		t.Error("the dry run removed a record")
	}

	yes := l.run(t, "runs", "purge", "--older-than", "1d", "--yes")
	wantExit(t, yes, 0)
	wantContains(t, "purge output", yes.stdout, "removed 1 records")
	if exists(recent) {
		t.Error("purge --yes kept the old record")
	}
	if !exists(real[0]) || !exists(broken) {
		t.Error("purge removed a fresh or unreadable record")
	}
}
