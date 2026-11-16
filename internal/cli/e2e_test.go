package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildPath string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// ralphBinary builds the ralph command once per test run.
func ralphBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end tests build and run the ralph binary; skipped under -short")
	}
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "ralph-e2e-")
		if buildErr != nil {
			return
		}
		buildPath = filepath.Join(buildDir, "ralph")
		cmd := exec.Command("go", "build", "-o", buildPath, "github.com/bengearig/ralph/cmd/ralph")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = errors.New("go build: " + err.Error() + "\n" + string(out))
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildPath
}

type e2eEnv struct {
	bin  string
	dir  string
	env  []string
	repo *testutil.Repo
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	bin := ralphBinary(t)
	repo := testutil.NewRepo(t)
	return &e2eEnv{
		bin:  bin,
		dir:  repo.Dir,
		repo: repo,
		env: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + t.TempDir(),
			"XDG_CONFIG_HOME=" + t.TempDir(),
			"GIT_CONFIG_GLOBAL=" + os.DevNull,
			"GIT_CONFIG_SYSTEM=" + os.DevNull,
		},
	}
}

func (e *e2eEnv) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Dir = e.dir
	cmd.Env = e.env
	return cmd
}

func (e *e2eEnv) init(t *testing.T) {
	t.Helper()
	out, err := e.command(context.Background(), "init").CombinedOutput()
	if err != nil {
		t.Fatalf("ralph init: %v\n%s", err, out)
	}
}

func (e *e2eEnv) connect(t *testing.T, stderr io.Writer, args ...string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cmd := e.command(ctx, append([]string{"mcp"}, args...)...)
	cmd.Stderr = stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func e2eCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func e2eOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := e2eCall(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: error result: %s", name, e2eText(res))
	}
	m, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v", name, res.StructuredContent)
	}
	return m
}

func e2eText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func e2eErrorCode(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("IsError = false, content %q", e2eText(res))
	}
	m, _ := res.StructuredContent.(map[string]any)
	code, _ := m["error_code"].(string)
	return code
}

func statusOf(t *testing.T, cs *mcp.ClientSession) map[string]any {
	t.Helper()
	return e2eOK(t, cs, "status", nil)
}

func TestE2EFullSession(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	if info := cs.InitializeResult().ServerInfo; info.Name != "ralph" {
		t.Errorf("server name = %q", info.Name)
	}
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"status", "unit_get", "unit_list", "unit_create", "unit_update", "task_set_state", "note_add", "link_add", "link_remove"} {
		if !names[want] {
			t.Errorf("tool %s not listed", want)
		}
	}

	empty := statusOf(t, cs)
	if briefs, _ := empty["briefs"].([]any); len(briefs) != 0 {
		t.Errorf("empty project has briefs: %v", empty["briefs"])
	}
	if tasks, _ := empty["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("empty project has tasks: %v", empty["tasks"])
	}

	e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Ship it", "body": "Please ship it."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "First slice"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Second slice"})
	t1 := e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-1", "title": "First task", "body": "do it", "acceptance_criteria": "it works"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-2", "title": "Second task", "acceptance_criteria": "it also works"})

	got := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if got["title"] != "First task" || got["status"] != "PROPOSED" || got["fingerprint"] != t1["fingerprint"] {
		t.Errorf("unit_get T-1 = %v", got)
	}

	upd := e2eOK(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": got["fingerprint"], "title": "Renamed task"})
	if upd["title"] != "Renamed task" || upd["fingerprint"] == got["fingerprint"] {
		t.Errorf("unit_update = %v", upd)
	}

	todo := e2eOK(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": upd["fingerprint"], "state": "TODO", "reason": "ready"})
	if todo["status"] != "TODO" {
		t.Errorf("task_set_state = %v", todo)
	}

	note := e2eOK(t, cs, "note_add", map[string]any{"id": "T-1", "body": "remember this"})
	if note["body"] != "remember this" {
		t.Errorf("note_add = %v", note)
	}
	links := e2eOK(t, cs, "link_add", map[string]any{"id": "B-1", "kind": "external", "target": "https://example.com/spec"})
	if l, _ := links["links"].([]any); len(l) != 1 {
		t.Errorf("link_add = %v", links)
	}

	final := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	if n, _ := final["notes"].([]any); len(n) < 2 {
		t.Errorf("T-1 notes = %v", final["notes"])
	}

	st := statusOf(t, cs)
	briefs, _ := st["briefs"].([]any)
	if len(briefs) != 1 {
		t.Fatalf("status briefs = %v", st["briefs"])
	}
	brief := briefs[0].(map[string]any)
	if brief["id"] != "B-1" || brief["status"] != "ARTICULATING" {
		t.Errorf("brief = %v", brief)
	}
	slices, _ := brief["slices"].([]any)
	if len(slices) != 2 {
		t.Fatalf("slices = %v", brief["slices"])
	}
	for _, s := range slices {
		sl := s.(map[string]any)
		want := "ARTICULATING"
		if sl["id"] == "S-1" {
			want = "READY"
		}
		if sl["status"] != want {
			t.Errorf("slice %v status = %v, want %s", sl["id"], sl["status"], want)
		}
	}
}

func TestE2EStaleUpdateRejected(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "title": "Orphan", "acceptance_criteria": "done"})
	read := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})
	e2eOK(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": read["fingerprint"], "title": "Mine"})

	res := e2eCall(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": read["fingerprint"], "title": "Theirs"})
	if code := e2eErrorCode(t, res); code != "stale" {
		t.Fatalf("error_code = %q, want stale (%s)", code, e2eText(res))
	}
	if !strings.Contains(e2eText(res), "unit_get") {
		t.Errorf("stale message does not name unit_get: %s", e2eText(res))
	}
	if got := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"}); got["title"] != "Mine" {
		t.Errorf("title = %v, want Mine", got["title"])
	}
}

// protocolSession writes requests to a raw ralph mcp process, closes its
// standard input, and returns every line it wrote to standard output.
func protocolSession(t *testing.T, e *e2eEnv, stderr *bytes.Buffer, args ...string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := e.command(ctx, append([]string{"mcp"}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	} {
		if _, err := io.WriteString(in, msg+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	// Let the server answer every request before the input ends.
	time.Sleep(2 * time.Second)
	_ = in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ralph mcp: %v\nstderr: %s", err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestE2EStdoutIsProtocolOnly(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	var stderr bytes.Buffer
	lines := protocolSession(t, e, &stderr, "--log-level", "debug")

	ids := map[float64]bool{}
	for _, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("stdout line is not a JSON-RPC message: %q", line)
		}
		if id, ok := msg["id"].(float64); ok {
			ids[id] = true
		}
	}
	for _, id := range []float64{1, 2, 3} {
		if !ids[id] {
			t.Errorf("no response for request %v; stdout = %q", id, lines)
		}
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Errorf("debug log lines are not on standard error: %q", stderr.String())
	}
	for _, line := range lines {
		if strings.Contains(line, "level=DEBUG") {
			t.Errorf("log line on standard output: %q", line)
		}
	}
}

func TestE2EExitsOnEndOfInput(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := e.command(ctx, "mcp")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader("")
	if err := cmd.Run(); err != nil {
		t.Fatalf("ralph mcp: %v\nstderr: %s", err, stderr.String())
	}
	if cmd.ProcessState.ExitCode() != 0 {
		t.Errorf("exit code = %d", cmd.ProcessState.ExitCode())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestE2ENotInitializedKeepsRunning(t *testing.T) {
	e := newE2EEnv(t)
	cs := e.connect(t, io.Discard)

	for range 2 {
		res := e2eCall(t, cs, "status", nil)
		if code := e2eErrorCode(t, res); code != "not_initialized" {
			t.Fatalf("error_code = %q, want not_initialized", code)
		}
		if !strings.Contains(e2eText(res), "ralph init") {
			t.Errorf("text lacks `ralph init`: %s", e2eText(res))
		}
	}
	if err := cs.Ping(context.Background(), nil); err != nil {
		t.Errorf("server stopped answering: %v", err)
	}
}

func TestE2EBrokenConfigExitsNonZero(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	e.repo.WriteFile(".ralph/project.yaml", "bogus: 1\n")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := e.command(ctx, "mcp")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("err = %v, want non-zero exit", err)
	}
	for _, want := range []string{"project.yaml", `"bogus"`} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestE2EGracefulSIGTERM(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := e.command(ctx, "mcp")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })

	// A tool call opens the database, so the shutdown has something to close.
	replies := bufio.NewScanner(out)
	replies.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for _, msg := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	} {
		if _, err := io.WriteString(in, msg+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	for want := 0; want < 2; want++ {
		if !replies.Scan() {
			t.Fatalf("server closed output early: %v\nstderr: %s", replies.Err(), stderr.String())
		}
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for replies.Scan() {
		}
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\nstderr: %s", err, stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("ralph mcp did not exit after SIGTERM")
	}
}

func TestE2EOutsideRepository(t *testing.T) {
	e := newE2EEnv(t)
	e.dir = t.TempDir()
	e.env = append(e.env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(e.dir))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := e.command(ctx, "mcp")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "not in a git repository") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestE2ENoticeLineNeedsASourceTheBinaryLacks(t *testing.T) {
	ralphBinary(t)
	t.Skip("no production notice source exists yet, so the `Notice for the user:` line is covered in-process in internal/mcpserver")
}

func TestE2EPlanningRules(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	fp := func(id string) string {
		t.Helper()
		got, _ := e2eOK(t, cs, "unit_get", map[string]any{"id": id})["fingerprint"].(string)
		return got
	}
	setState := func(id, state, reason string) *mcp.CallToolResult {
		t.Helper()
		args := map[string]any{"id": id, "fingerprint": fp(id), "state": state}
		if reason != "" {
			args["reason"] = reason
		}
		return e2eCall(t, cs, "task_set_state", args)
	}
	mustMove := func(id, state, reason string) {
		t.Helper()
		if res := setState(id, state, reason); res.IsError {
			t.Fatalf("%s to %s: %s", id, state, e2eText(res))
		}
	}
	mustFail := func(id, state, reason, code string) *mcp.CallToolResult {
		t.Helper()
		res := setState(id, state, reason)
		if got := e2eErrorCode(t, res); got != code {
			t.Fatalf("%s to %s: error_code = %q (%s), want %q", id, state, got, e2eText(res), code)
		}
		return res
	}
	statusText := func() string {
		t.Helper()
		out, err := e.command(context.Background(), "status").CombinedOutput()
		if err != nil {
			t.Fatalf("ralph status: %v\n%s", err, out)
		}
		return string(out)
	}

	e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Plan", "body": "Plan the work."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "First slice"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Second slice"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-1", "title": "First task", "body": "do the first"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-2", "title": "Second task", "body": "do the second", "acceptance_criteria": "it works"})

	res := mustFail("T-1", "TODO", "", "not_ready")
	if missing, _ := res.StructuredContent.(map[string]any)["missing"].([]any); len(missing) == 0 {
		t.Errorf("not_ready lists nothing missing: %v", res.StructuredContent)
	}
	e2eOK(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": fp("T-1"), "acceptance_criteria": "the first works"})
	mustMove("T-1", "TODO", "")

	mustFail("T-1", "BLOCKED", "", "reason_required")
	mustMove("T-1", "BLOCKED", "waiting for an answer")
	mustMove("T-1", "TODO", "")

	mustFail("T-1", "PROPOSED", "", "reason_required")
	mustMove("T-1", "PROPOSED", "the plan around it is wrong")
	var kickback bool
	notes, _ := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"})["notes"].([]any)
	for _, n := range notes {
		if m, _ := n.(map[string]any); m["kind"] == "KICKBACK" && strings.Contains(m["body"].(string), "the plan around it is wrong") {
			kickback = true
		}
	}
	if !kickback {
		t.Errorf("no KICKBACK note in %v", notes)
	}
	mustMove("T-1", "TODO", "")

	dep := e2eOK(t, cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	if w, _ := dep["waiting_on"].([]any); len(w) != 1 || w[0] != "S-1" {
		t.Errorf("dependency_add = %v", dep)
	}
	if out := statusText(); !strings.Contains(out, "waiting on S-1") {
		t.Errorf("status while waiting:\n%s", out)
	}

	mustMove("T-2", "TODO", "")
	res = mustFail("T-2", "IN_PROGRESS", "", "waiting")
	waiting, _ := res.StructuredContent.(map[string]any)["waiting_on"].([]any)
	if len(waiting) != 1 || waiting[0].(map[string]any)["id"] != "S-1" {
		t.Errorf("waiting_on = %v", waiting)
	}

	cycle := e2eCall(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "S-2"})
	if got := e2eErrorCode(t, cycle); got != "invalid" || !strings.Contains(e2eText(cycle), "cycle") {
		t.Errorf("cycle: %q %s", got, e2eText(cycle))
	}

	mustMove("T-1", "IN_PROGRESS", "")
	mustMove("T-1", "DONE", "")
	s2 := e2eOK(t, cs, "unit_get", map[string]any{"id": "S-2"})
	deps, _ := s2["depends_on"].([]any)
	if len(deps) != 1 || deps[0].(map[string]any)["satisfied"] != true || s2["waiting_on"] != nil {
		t.Errorf("S-2 after S-1 is done = %v", s2)
	}
	mustMove("T-2", "IN_PROGRESS", "")
	if out := statusText(); strings.Contains(out, "waiting on") {
		t.Errorf("status after the dependency is satisfied:\n%s", out)
	}
}
