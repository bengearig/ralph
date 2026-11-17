package loop_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

var (
	recStart = time.Date(2026, 11, 17, 9, 30, 5, 0, time.UTC)
	recClock = func() time.Time { return recStart }
)

func recInfo() loop.AttemptInfo {
	return loop.AttemptInfo{
		Slice: "M-19", Task: "T-90", Attempt: 3, Harness: "claude-code", Model: "opus",
		Mode: harness.PermissionRestricted, StartHead: "aaa111", Started: recStart,
	}
}

func newRec(t *testing.T, secrets ...string) (loop.AttemptRecord, string) {
	t.Helper()
	root := t.TempDir()
	r, err := loop.NewRecorder(loop.RecorderOptions{Root: root, Secrets: secrets, Now: recClock}).Begin(ctx, recInfo())
	if err != nil {
		t.Fatal(err)
	}
	return r, root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func failedResult() (loop.AttemptResult, headless.Result) {
	rep := verify.Report{Results: []verify.Result{
		{Command: "go build ./...", ExitCode: 0, Duration: 1500 * time.Millisecond, Output: "ok\n"},
		{Command: "go test ./...", ExitCode: 1, Duration: 20 * time.Second, Output: "FAIL one\nFAIL two\n"},
	}}
	run := headless.Result{
		ExitCode: 0, Duration: 2 * time.Minute, Notes: []string{"a note"},
		Outcome: harness.Outcome{
			Denials: []harness.Denial{{Tool: "Bash", Target: "git push", Reason: "not allowed", Shell: true}},
			Usage:   harness.Usage{CostUSD: 0.31, CostReported: true, InputTokens: 10, OutputTokens: 20, ReasoningTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5},
		},
	}
	res := loop.AttemptResult{
		Outcome: loop.OutcomeFailed, Failure: "verification failed: go test ./... (exit 1)",
		EndHead: "bbb222", Duration: 130 * time.Second, Verification: rep, Run: run,
		Prompt: loop.Prompt{Documents: []loop.DocumentUse{
			{ID: "D-1", Title: "Design", Included: true},
			{ID: "D-2", Title: "Big", Reason: "over the limit"},
		}},
	}
	return res, run
}

func TestRecordCreatesDirectoryAndFiles(t *testing.T) {
	r, root := newRec(t)
	if err := r.Prompt("hello"); err != nil {
		t.Fatal(err)
	}
	if err := r.Verification(verify.Report{}); err != nil {
		t.Fatal(err)
	}
	path, err := r.Finish(loop.AttemptResult{}, headless.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if want := ".ralph/runs/M-19/20261117-093005-003-T-90"; path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	dir := filepath.Join(root, filepath.FromSlash(path))
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("dir = %v, %v", fi, err)
	}
	for _, name := range []string{"prompt.md", "output.jsonl", "verify.txt", "record.json"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %v", name, fi.Mode().Perm())
		}
	}
}

func TestRecordNameCollisionGetsSuffix(t *testing.T) {
	root := t.TempDir()
	rec := loop.NewRecorder(loop.RecorderOptions{Root: root, Now: recClock})
	var paths []string
	for range 3 {
		r, err := rec.Begin(ctx, recInfo())
		if err != nil {
			t.Fatal(err)
		}
		p, err := r.Finish(loop.AttemptResult{}, headless.Result{})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	base := ".ralph/runs/M-19/20261117-093005-003-T-90"
	want := []string{base, base + "-2", base + "-3"}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestRecordPromptAndOutputWritten(t *testing.T) {
	r, root := newRec(t)
	if err := r.Prompt("the prompt\n"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, ".ralph/runs/M-19/20261117-093005-003-T-90/output.jsonl")
	if _, err := r.Stdout().Write([]byte("{\"a\":1}\n{\"b\"")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, out); got != "{\"a\":1}\n" {
		t.Errorf("before the second line completes: %q", got)
	}
	put(t, r, []byte(":2}\n"))
	if got := readFile(t, out); got != "{\"a\":1}\n{\"b\":2}\n" {
		t.Errorf("before Finish: %q", got)
	}
	path, err := r.Finish(loop.AttemptResult{}, headless.Result{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, path)
	if got := readFile(t, filepath.Join(dir, "prompt.md")); got != "the prompt\n" {
		t.Errorf("prompt.md = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "output.jsonl")); got != "{\"a\":1}\n{\"b\":2}\n" {
		t.Errorf("output.jsonl = %q", got)
	}
}

func TestRecordVerifyFileFormat(t *testing.T) {
	r, root := newRec(t)
	res, _ := failedResult()
	res.Verification.Results = append(res.Verification.Results,
		verify.Result{Command: "sleep 99", ExitCode: -1, TimedOut: true, Duration: time.Minute})
	if err := r.Verification(res.Verification); err != nil {
		t.Fatal(err)
	}
	golden(t, "record_verify.golden", readFile(t, filepath.Join(root, ".ralph/runs/M-19/20261117-093005-003-T-90/verify.txt")))
}

func TestRecordVerifyFileWithoutCommands(t *testing.T) {
	r, root := newRec(t)
	if err := r.Verification(verify.Report{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(root, ".ralph/runs/M-19/20261117-093005-003-T-90/verify.txt"))
	if got != "no verification commands configured\n" {
		t.Errorf("verify.txt = %q", got)
	}
}

func TestRecordJSONGolden(t *testing.T) {
	r, root := newRec(t)
	res, run := failedResult()
	path, err := r.Finish(res, run)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "record_failed.golden", readFile(t, filepath.Join(root, path, "record.json")))
}

func TestRecordJSONDoneAttempt(t *testing.T) {
	r, root := newRec(t)
	res := loop.AttemptResult{Outcome: loop.OutcomeDone, Commit: "ccc333", EndHead: "ccc333", Duration: 1500 * time.Millisecond}
	path, err := r.Finish(res, headless.Result{})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "record_done.golden", readFile(t, filepath.Join(root, path, "record.json")))
}

func TestRecordRelativeSlashedPath(t *testing.T) {
	r, _ := newRec(t)
	path, err := r.Finish(loop.AttemptResult{}, headless.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(path) || strings.Contains(path, `\`) || !strings.HasPrefix(path, ".ralph/runs/") {
		t.Errorf("path = %q", path)
	}
}

func TestRecordWriteFailureReturnsErrorAndContinues(t *testing.T) {
	r, root := newRec(t)
	dir := filepath.Join(root, ".ralph/runs/M-19/20261117-093005-003-T-90")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced")
	}
	if err := r.Prompt("x"); err == nil {
		t.Error("Prompt: no error")
	}
	if err := r.Verification(verify.Report{}); err == nil {
		t.Error("Verification: no error")
	}
	if _, err := r.Stdout().Write([]byte("line\n")); err != nil {
		t.Errorf("Stdout write: %v", err)
	}
	if _, err := r.Finish(loop.AttemptResult{}, headless.Result{}); err == nil {
		t.Error("Finish: no error")
	}
}

func TestRecordBeginFailureReturnsError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".ralph"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := loop.NewRecorder(loop.RecorderOptions{Root: root}).Begin(ctx, recInfo())
	if err == nil || r != nil {
		t.Errorf("Begin = %v, %v", r, err)
	}
}

func TestRecordJSONAtomic(t *testing.T) {
	r, root := newRec(t)
	path, err := r.Finish(loop.AttemptResult{}, headless.Result{})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || strings.Contains(e.Name(), "tmp") {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

func TestRecordOnlyInsideRunsDirectory(t *testing.T) {
	for _, c := range []struct{ slice, task string }{
		{"../x", "T-1"}, {"a/b", "T-1"}, {"M-1", "../../T"}, {"M-1", "a/b"}, {"..", "T-1"}, {"", "T-1"}, {"M-1", ""}, {`a\b`, "T-1"},
	} {
		root := t.TempDir()
		info := recInfo()
		info.Slice, info.Task = c.slice, c.task
		r, err := loop.NewRecorder(loop.RecorderOptions{Root: root, Now: recClock}).Begin(ctx, info)
		if err == nil || r != nil {
			t.Errorf("%+v: Begin = %v, %v", c, r, err)
		}
		if _, statErr := os.Stat(filepath.Join(root, ".ralph")); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("%+v: wrote under the root: %v", c, statErr)
		}
	}
}

const secret = "s3cr3t-token-value"

func TestRedactReplacesSecretInAllFiles(t *testing.T) {
	r, root := newRec(t, secret)
	res, run := failedResult()
	res.Failure = "failed with " + secret
	res.Verification.Results[1].Output = "output " + secret
	run.Notes = []string{"note " + secret}
	run.Outcome.Denials[0].Target = "curl " + secret
	if err := r.Prompt("prompt " + secret); err != nil {
		t.Fatal(err)
	}
	put(t, r, []byte("out "+secret+"\n"))
	if err := r.Verification(res.Verification); err != nil {
		t.Fatal(err)
	}
	path, err := r.Finish(res, run)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prompt.md", "output.jsonl", "verify.txt", "record.json"} {
		got := readFile(t, filepath.Join(root, path, name))
		if strings.Contains(got, secret) {
			t.Errorf("%s holds the secret", name)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Errorf("%s has no replacement", name)
		}
	}
}

func TestRedactIgnoresShortValues(t *testing.T) {
	r, root := newRec(t, "1234567", "12345678")
	put(t, r, []byte("a 1234567 b 12345678 c\n"))
	path, _ := r.Finish(loop.AttemptResult{}, headless.Result{})
	if got := readFile(t, filepath.Join(root, path, "output.jsonl")); got != "a 1234567 b [redacted] c\n" {
		t.Errorf("output = %q", got)
	}
}

func TestRedactSecretSplitAcrossWrites(t *testing.T) {
	r, root := newRec(t, secret)
	for _, chunk := range []string{"x s3cr3t-", "token", "-value y\n"} {
		put(t, r, []byte(chunk))
	}
	path, _ := r.Finish(loop.AttemptResult{}, headless.Result{})
	if got := readFile(t, filepath.Join(root, path, "output.jsonl")); got != "x [redacted] y\n" {
		t.Errorf("output = %q", got)
	}
}

func TestRedactFlushesFinalPartialLine(t *testing.T) {
	r, root := newRec(t, secret)
	put(t, r, []byte("tail "+secret))
	path, _ := r.Finish(loop.AttemptResult{}, headless.Result{})
	if got := readFile(t, filepath.Join(root, path, "output.jsonl")); got != "tail [redacted]" {
		t.Errorf("output = %q", got)
	}
}

func TestRedactVeryLongLine(t *testing.T) {
	r, root := newRec(t, secret)
	w := r.Stdout()
	long := strings.Repeat("a", 1<<20)
	putW(t, w, []byte(long+"s3cr3t-"))
	putW(t, w, []byte("token-value"+long))
	path, _ := r.Finish(loop.AttemptResult{}, headless.Result{})
	got := readFile(t, filepath.Join(root, path, "output.jsonl"))
	if strings.Contains(got, secret) || !strings.Contains(got, "a[redacted]a") || len(got) != 2*len(long)+len("[redacted]") {
		t.Errorf("long line mishandled: %d bytes", len(got))
	}
}

func TestRedactNote(t *testing.T) {
	res := loop.AttemptResult{Outcome: loop.OutcomeFailed, Failure: "bad " + secret, RecordErrors: []string{"x " + secret}}
	got := loop.AttemptSummary(1, 3, res, []string{secret})
	if strings.Contains(got, secret) || strings.Count(got, "[redacted]") != 2 {
		t.Errorf("note = %q", got)
	}
}

func TestSecretValuesFromConfig(t *testing.T) {
	withEnv := func(env map[string]string) *config.Config { return loadConfigEnv(t, t.TempDir(), env) }
	got := loop.SecretValues(withEnv(map[string]string{"GITHUB_TOKEN": "gh-token-12345", "BITBUCKET_TOKEN": "bb-token-67890"}))
	if len(got) != 2 || got[0] != "bb-token-67890" && got[1] != "bb-token-67890" {
		t.Errorf("both set: %v", got)
	}
	got = loop.SecretValues(withEnv(map[string]string{"GITHUB_TOKEN": "gh-token-12345"}))
	if len(got) != 1 || got[0] != "gh-token-12345" {
		t.Errorf("one set: %v", got)
	}
	got = loop.SecretValues(withEnv(map[string]string{"GITHUB_TOKEN": "same-value-1", "BITBUCKET_TOKEN": "same-value-1"}))
	if len(got) != 1 {
		t.Errorf("duplicates: %v", got)
	}
	if got := loop.SecretValues(withEnv(nil)); len(got) != 0 {
		t.Errorf("none set: %v", got)
	}
	if got := loop.SecretValues(nil); len(got) != 0 {
		t.Errorf("nil config: %v", got)
	}
}

func TestAttemptSummaryDone(t *testing.T) {
	res := loop.AttemptResult{
		Outcome: loop.OutcomeDone, Commit: "0123456789abcdef0123", Duration: 130*time.Second + 400*time.Millisecond,
		RecordPath: ".ralph/runs/M-1/x", Run: headless.Result{Outcome: harness.Outcome{Usage: harness.Usage{CostUSD: 0.306, CostReported: true}}},
	}
	want := "Attempt 2 of 3 passed: commit 0123456789ab (2m10s, $0.31)\nRun record: .ralph/runs/M-1/x"
	if got := loop.AttemptSummary(2, 3, res, nil); got != want {
		t.Errorf("summary = %q", got)
	}
}

func TestAttemptSummaryFailedWithDenialSuggestions(t *testing.T) {
	res, run := failedResult()
	res.Run, res.RecordPath = run, ".ralph/runs/M-1/x"
	run.Outcome.Denials = append(run.Outcome.Denials, run.Outcome.Denials[0], harness.Denial{Tool: "Write"})
	res.Run = run
	got := loop.AttemptSummary(1, 3, res, nil)
	want := []string{
		"Attempt 1 of 3 failed: verification failed: go test ./... (exit 1) (2m10s, $0.31)",
		"Run record: .ralph/runs/M-1/x",
		headless.Suggest(run.Outcome.Denials[0]),
		headless.Suggest(run.Outcome.Denials[2]),
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("summary = %q", got)
	}
}

func TestAttemptSummaryCostOmittedWhenNotReported(t *testing.T) {
	res := loop.AttemptResult{Outcome: loop.OutcomeFailed, Failure: "nope", Duration: time.Second,
		Run: headless.Result{Outcome: harness.Outcome{Usage: harness.Usage{CostUSD: 5}}}}
	if got := loop.AttemptSummary(1, 3, res, nil); got != "Attempt 1 of 3 failed: nope (1s)" {
		t.Errorf("summary = %q", got)
	}
}

func TestAttemptSummaryRecordError(t *testing.T) {
	res := loop.AttemptResult{Outcome: loop.OutcomeFailed, Failure: "nope", RecordErrors: []string{"begin the record: boom", "finish the record: bang"}}
	want := "Attempt 1 of 3 failed: nope (0s)\nRun record could not be written: begin the record: boom\nRun record could not be written: finish the record: bang"
	if got := loop.AttemptSummary(1, 3, res, nil); got != want {
		t.Errorf("summary = %q", got)
	}
}

func TestAttemptNoteIsSummary(t *testing.T) {
	a := newAttemptFixture(t)
	a.cfg = loadConfigEnv(t, a.repo.Dir, map[string]string{"GITHUB_TOKEN": "gh-token-12345"})
	a.deps.Config = a.cfg
	a.deps.Recorder = loop.NewRecorder(loop.RecorderOptions{Root: a.repo.Dir, Secrets: loop.SecretValues(a.cfg)})
	a.result.Outcome.Denials = []harness.Denial{{Tool: "Bash", Target: "gh-token-12345", Shell: true}}
	a.result.Outcome.Success = false
	res := a.attempt(t)
	if n := a.noteCount(t, units.NoteGeneral); n != 1 {
		t.Fatalf("attempt notes = %d", n)
	}
	note := a.attemptNote(t)
	lines := strings.Split(note, "\n")
	if !strings.HasPrefix(lines[0], "Attempt 1 of 3 failed: ") || !strings.HasSuffix(lines[0], "(0s)") {
		t.Errorf("first line = %q", lines[0])
	}
	if res.RecordPath == "" || !strings.Contains(note, "Run record: "+res.RecordPath) {
		t.Errorf("note = %q, record = %q", note, res.RecordPath)
	}
	if strings.Contains(note, "gh-token-12345") || !strings.Contains(note, "[redacted]") {
		t.Errorf("note = %q", note)
	}
	if _, err := os.Stat(filepath.Join(a.repo.Dir, res.RecordPath, "record.json")); err != nil {
		t.Error(err)
	}
}

func loadConfigEnv(t *testing.T, root string, env map[string]string) *config.Config {
	t.Helper()
	c, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func put(t *testing.T, r loop.AttemptRecord, b []byte) {
	t.Helper()
	putW(t, r.Stdout(), b)
}

func putW(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
}
