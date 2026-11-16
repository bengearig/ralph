package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const neutralApproval = "Show the outline to the user in this conversation and wait."

// withHarnesses puts the named fake harness executables first on the binary's PATH.
func (e *e2eEnv) withHarnesses(t *testing.T, tools map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for name, script := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, kv := range e.env {
		if rest, ok := strings.CutPrefix(kv, "PATH="); ok {
			e.env[i] = "PATH=" + bin + string(os.PathListSeparator) + rest
		}
	}
}

func (e *e2eEnv) writeWorkflow(t *testing.T, name, content string) {
	t.Helper()
	writeWorkflowFile(t, e.dir, name, content)
}

func (e *e2eEnv) ralph(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	q := &quickE2E{e2eEnv: e}
	return q.run(t, args...)
}

func e2eFingerprint(t *testing.T, cs *mcp.ClientSession, id string) string {
	t.Helper()
	fp, _ := e2eOK(t, cs, "unit_get", map[string]any{"id": id})["fingerprint"].(string)
	return fp
}

func e2eNotes(t *testing.T, cs *mcp.ClientSession, id string) []map[string]any {
	t.Helper()
	raw, _ := e2eOK(t, cs, "unit_get", map[string]any{"id": id})["notes"].([]any)
	var out []map[string]any
	for _, n := range raw {
		m, _ := n.(map[string]any)
		out = append(out, m)
	}
	return out
}

func hasNote(notes []map[string]any, kind, text string) bool {
	for _, n := range notes {
		if n["kind"] == kind && strings.Contains(n["body"].(string), text) {
			return true
		}
	}
	return false
}

func TestE2EReceiveArticulateFlow(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	settings := e2eOK(t, cs, "planning_settings", nil)
	if settings["confirm_outline"] != false || settings["revisions_always_ask"] != true {
		t.Fatalf("planning_settings = %v", settings)
	}

	const verbatim = "Build the thing.\n  Keep my  odd   spacing, and this line.\n"
	e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "The thing", "body": verbatim})
	for _, note := range []string{"Goal: users can do X.", "Non-goal: no Y."} {
		e2eOK(t, cs, "note_add", map[string]any{"id": "B-1", "body": note})
	}
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Foundation"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Surface"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-1", "title": "Add the store", "body": "Summary of the store."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-1", "title": "Add the loader", "body": "Summary of the loader."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-2", "title": "Add the screen", "body": "Summary of the screen."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-2", "title": "Add the help", "body": "Summary of the help."})
	dep := e2eOK(t, cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	if w, _ := dep["waiting_on"].([]any); len(w) != 1 || w[0] != "S-1" {
		t.Errorf("dependency_add = %v", dep)
	}

	brief := e2eOK(t, cs, "unit_get", map[string]any{"id": "B-1"})
	if brief["body"] != verbatim {
		t.Errorf("brief body = %q, want %q", brief["body"], verbatim)
	}
	if notes := e2eNotes(t, cs, "B-1"); !hasNote(notes, "NOTE", "Goal: users can do X.") || !hasNote(notes, "NOTE", "Non-goal: no Y.") {
		t.Errorf("brief notes = %v", notes)
	}

	st := statusOf(t, cs)
	briefs, _ := st["briefs"].([]any)
	if len(briefs) != 1 {
		t.Fatalf("status briefs = %v", st["briefs"])
	}
	b := briefs[0].(map[string]any)
	if b["id"] != "B-1" || b["status"] != "ARTICULATING" {
		t.Errorf("brief in status = %v", b)
	}
	sliceStatus := func(id string) map[string]any {
		t.Helper()
		st := statusOf(t, cs)
		for _, bb := range st["briefs"].([]any) {
			for _, s := range bb.(map[string]any)["slices"].([]any) {
				if sm := s.(map[string]any); sm["id"] == id {
					return sm
				}
			}
		}
		t.Fatalf("slice %s not in status", id)
		return nil
	}
	s2 := sliceStatus("S-2")
	if w, _ := s2["waiting_on"].([]any); len(w) != 1 || w[0] != "S-1" {
		t.Errorf("status does not show S-2 waiting on S-1: %v", s2)
	}
	if counts, _ := sliceStatus("S-1")["tasks"].(map[string]any); counts["PROPOSED"] != float64(2) {
		t.Errorf("S-1 task counts = %v, want two PROPOSED", counts)
	}
	for _, id := range []string{"T-1", "T-2", "T-3", "T-4"} {
		if got := e2eOK(t, cs, "unit_get", map[string]any{"id": id}); got["status"] != "PROPOSED" {
			t.Errorf("%s status = %v, want PROPOSED", id, got["status"])
		}
	}
	if out, _ := statusTextOf(t, e); !strings.Contains(out, "waiting on S-1") {
		t.Errorf("ralph status does not show S-2 waiting:\n%s", out)
	}

	move := func(id, state, reason string) *mcp.CallToolResult {
		args := map[string]any{"id": id, "fingerprint": e2eFingerprint(t, cs, id), "state": state}
		if reason != "" {
			args["reason"] = reason
		}
		return e2eCall(t, cs, "task_set_state", args)
	}
	articulate := func(id, body, criteria string) {
		t.Helper()
		e2eOK(t, cs, "unit_update", map[string]any{"id": id, "fingerprint": e2eFingerprint(t, cs, id), "body": body, "acceptance_criteria": criteria})
	}

	res := move("T-1", "TODO", "worked out")
	if code := e2eErrorCode(t, res); code != "not_ready" {
		t.Fatalf("promotion without criteria: error_code = %q", code)
	}
	if missing, _ := res.StructuredContent.(map[string]any)["missing"].([]any); len(missing) == 0 {
		t.Errorf("not_ready lists nothing missing: %v", res.StructuredContent)
	}
	articulate("T-1", "Detailed store work.", "The store opens and closes.")
	articulate("T-2", "Detailed loader work.", "The loader reads the store.")
	for _, id := range []string{"T-1", "T-2"} {
		if r := move(id, "TODO", "worked out with the user"); r.IsError {
			t.Fatalf("%s to TODO: %s", id, e2eText(r))
		}
	}

	if r := move("T-2", "PROPOSED", "the loader belongs in the next slice"); r.IsError {
		t.Fatalf("kickback: %s", e2eText(r))
	}
	e2eOK(t, cs, "note_add", map[string]any{"id": "T-2", "kind": "KICKBACK", "body": "Needs a different shape of loader."})
	got := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-2"})
	if got["status"] != "PROPOSED" {
		t.Errorf("T-2 status = %v", got["status"])
	}
	notes := e2eNotes(t, cs, "T-2")
	if !hasNote(notes, "KICKBACK", "the loader belongs in the next slice") || !hasNote(notes, "KICKBACK", "Needs a different shape of loader.") {
		t.Errorf("T-2 notes = %v", notes)
	}
	if s1 := sliceStatus("S-1"); s1["status"] == "READY" {
		t.Errorf("S-1 READY with a PROPOSED task: %v", s1)
	}

	if r := move("T-4", "CANCELLED", "the help is out of scope"); r.IsError {
		t.Fatalf("cancel: %s", e2eText(r))
	}
	e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Polish"})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-3", "title": "Tidy up", "body": "Summary."})
	if !hasNote(e2eNotes(t, cs, "T-4"), "NOTE", "the help is out of scope") {
		t.Errorf("cancel reason missing from T-4 notes: %v", e2eNotes(t, cs, "T-4"))
	}
	if !hasNote(e2eNotes(t, cs, "T-2"), "KICKBACK", "the loader belongs in the next slice") {
		t.Error("kickback history lost after the revision")
	}
	if got := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-4"}); got["status"] != "CANCELLED" || got["title"] != "Add the help" {
		t.Errorf("T-4 after cancel = %v", got)
	}
	if got := e2eOK(t, cs, "unit_get", map[string]any{"id": "B-1"}); got["body"] != verbatim {
		t.Errorf("brief body changed to %q", got["body"])
	}
	if notes := e2eNotes(t, cs, "B-1"); len(notes) < 2 {
		t.Errorf("brief notes lost: %v", notes)
	}

	articulate("T-2", "Detailed loader work, reshaped.", "The loader reads the store lazily.")
	if r := move("T-2", "TODO", "reworked"); r.IsError {
		t.Fatalf("T-2 to TODO again: %s", e2eText(r))
	}
	if s1 := sliceStatus("S-1"); s1["status"] != "READY" {
		t.Errorf("S-1 = %v, want READY", s1)
	}
}

func statusTextOf(t *testing.T, e *e2eEnv) (string, string) {
	t.Helper()
	code, out, errOut := e.ralph(t, "status")
	if code != 0 {
		t.Fatalf("ralph status: exit %d\n%s\n%s", code, out, errOut)
	}
	return out, errOut
}

func TestE2EPlanningSettingFromConfig(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	path := filepath.Join(e.dir, ".ralph", "project.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, []byte("plan:\n  confirm_outline: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := e.connect(t, io.Discard)
	if got := e2eOK(t, cs, "planning_settings", nil); got["confirm_outline"] != true || got["revisions_always_ask"] != true {
		t.Errorf("from the project file: %v", got)
	}
	_ = cs.Close()

	e.env = append(e.env, "RALPH_PLAN_CONFIRM_OUTLINE=false")
	cs = e.connect(t, io.Discard)
	if got := e2eOK(t, cs, "planning_settings", nil); got["confirm_outline"] != false || got["revisions_always_ask"] != true {
		t.Errorf("with the environment override: %v", got)
	}
}

var (
	claudeToolRef   = regexp.MustCompile("`mcp__ralph__([a-z_]+)`")
	opencodeToolRef = regexp.MustCompile("`([a-z_]+)` tool")
)

func TestE2EInstructionsForBothHarnesses(t *testing.T) {
	e := newE2EEnv(t)
	e.withHarnesses(t, map[string]string{"claude": fakeClaudeScript, "opencode": fakeOpenCodeScript})
	e.init(t)

	listed, err := e.connect(t, io.Discard).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exist := map[string]bool{}
	for _, tool := range listed.Tools {
		exist[tool.Name] = true
	}

	instructions, err := workflow.All()
	if err != nil || len(instructions) == 0 {
		t.Fatalf("workflow.All: %d, %v", len(instructions), err)
	}
	harnesses := []struct {
		dir string
		ref *regexp.Regexp
	}{
		{".claude/commands/ralph", claudeToolRef},
		{".opencode/commands/ralph", opencodeToolRef},
	}
	for _, ins := range instructions {
		for _, h := range harnesses {
			p := filepath.Join(e.dir, h.dir, ins.Name+".md")
			data, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			var named []string
			for _, m := range h.ref.FindAllStringSubmatch(string(data), -1) {
				named = append(named, m[1])
			}
			slices.Sort(named)
			named = slices.Compact(named)
			if !slices.Equal(named, ins.Tools()) {
				t.Errorf("%s names tools %v, the instruction uses %v", p, named, ins.Tools())
			}
			for _, name := range named {
				if !exist[name] {
					t.Errorf("%s names %s, which ralph mcp does not list", p, name)
				}
			}
		}
	}

	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(e.dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, name := range []string{"receive", "articulate"} {
		for _, h := range harnesses {
			if _, err := os.Stat(filepath.Join(e.dir, h.dir, name+".md")); err != nil {
				t.Errorf("%s/%s.md: %v", h.dir, name, err)
			}
		}
	}
	claudeReceive := read(".claude/commands/ralph/receive.md")
	for _, want := range []string{"EnterPlanMode", "ExitPlanMode"} {
		if !strings.Contains(claudeReceive, want) {
			t.Errorf("Claude Code receive.md lacks %s", want)
		}
	}
	opencodeReceive := read(".opencode/commands/ralph/receive.md")
	for _, bad := range []string{"EnterPlanMode", "ExitPlanMode"} {
		if strings.Contains(opencodeReceive, bad) {
			t.Errorf("OpenCode receive.md contains %s", bad)
		}
	}
	if !strings.Contains(opencodeReceive, neutralApproval) {
		t.Errorf("OpenCode receive.md lacks the neutral approval sentence")
	}

	code, out, errOut := e.ralph(t, "init")
	if code != 0 {
		t.Fatalf("second init: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, prefix := range []string{"installed ", "updated ", "adopted ", "kept ", "skipped "} {
		if n := countPrefix(out, prefix); n != 0 {
			t.Errorf("second init printed %d %q lines:\n%s", n, prefix, out)
		}
	}
	if countPrefix(out, "unchanged ") == 0 {
		t.Errorf("second init printed no unchanged lines:\n%s", out)
	}
}

func TestE2EWorkflowOverrides(t *testing.T) {
	e := newE2EEnv(t)
	e.withHarnesses(t, map[string]string{"claude": fakeClaudeScript, "opencode": fakeOpenCodeScript})
	const appended = "Always mention the quarterly review."
	const replaced = "A replacement status body, distinctive."
	e.writeWorkflow(t, "receive.append.md", appended+"\n")
	e.writeWorkflow(t, "status.md", replaced+"\n")

	code, out, errOut := e.ralph(t, "init")
	if code != 0 {
		t.Fatalf("init: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"workflow: receive extended by .ralph/workflow/receive.append.md",
		"workflow: status replaced by .ralph/workflow/status.md",
	} {
		if !containsLine(outLines(out), want) {
			t.Errorf("init output lacks %q:\n%s", want, out)
		}
	}
	for _, dir := range []string{".claude/commands/ralph", ".opencode/commands/ralph"} {
		for name, want := range map[string]string{"receive": appended, "status": replaced} {
			data, err := os.ReadFile(filepath.Join(e.dir, dir, name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), want) {
				t.Errorf("%s/%s.md lacks %q", dir, name, want)
			}
		}
	}
	if out, _ := statusTextOf(t, e); strings.Contains(out, "out of date") {
		t.Errorf("status out of date right after init:\n%s", out)
	}

	e.writeWorkflow(t, "receive.append.md", "A different appended sentence.\n")
	if out, _ := statusTextOf(t, e); !strings.Contains(out, integrationOutdatedLine) {
		t.Errorf("status lacks the out-of-date line after editing the override:\n%s", out)
	}
	if code, out, errOut := e.ralph(t, "init"); code != 0 {
		t.Fatalf("init after edit: exit %d\n%s\n%s", code, out, errOut)
	}
	if out, _ := statusTextOf(t, e); strings.Contains(out, "out of date") {
		t.Errorf("init did not clear the out-of-date line:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(e.dir, ".claude/commands/ralph/receive.md"))
	if err != nil || !strings.Contains(string(data), "A different appended sentence.") || strings.Contains(string(data), appended) {
		t.Errorf("receive.md after edit: %v\n%s", err, data)
	}
}

func TestE2EWorkflowOverrideUnknownName(t *testing.T) {
	e := newE2EEnv(t)
	e.withHarnesses(t, map[string]string{"claude": fakeClaudeScript})
	e.writeWorkflow(t, "bogus.md", "text\n")
	code, out, errOut := e.ralph(t, "init")
	if code == 0 || !strings.Contains(errOut, "bogus.md") {
		t.Fatalf("init: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, p := range []string{".ralph/store.db", ".ralph/integration.json", ".claude", "CLAUDE.md", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(e.dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s exists (%v)", p, err)
		}
	}
}
