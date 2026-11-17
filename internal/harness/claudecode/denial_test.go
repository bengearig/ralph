package claudecode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func use(id, name, input string) string {
	return `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`
}

func denyEvent(id, name string) string {
	return `{"type":"system","subtype":"permission_denied","tool_name":"` + name + `","tool_use_id":"` + id + `","message":"Permission to use ` + name + ` has been denied"}`
}

func lines(l ...string) string { return strings.Join(l, "\n") }

func TestClaudeParserDenialFromEventUsesRememberedInput(t *testing.T) {
	out := parse(lines(use("t1", "Bash", `{"command":"echo hi > a.txt"}`), denyEvent("t1", "Bash")))
	want := []harness.Denial{{Tool: "Bash", Target: "echo hi > a.txt", Reason: "Permission to use Bash has been denied", Shell: true}}
	if !reflect.DeepEqual(out.Denials, want) {
		t.Errorf("Denials = %+v, want %+v", out.Denials, want)
	}
}

func TestClaudeParserDenialFromResultOnly(t *testing.T) {
	out := parse(`{"type":"result","subtype":"success","is_error":false,"permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"rm -rf x"}}],"result":"ok"}`)
	want := []harness.Denial{{Tool: "Bash", Target: "rm -rf x", Reason: "denied by the permission mode", Shell: true}}
	if !reflect.DeepEqual(out.Denials, want) {
		t.Errorf("Denials = %+v, want %+v", out.Denials, want)
	}
}

func TestClaudeParserDenialEventAndResultNotDuplicated(t *testing.T) {
	out := parse(fixture(t, "denied.jsonl"))
	if len(out.Denials) != 1 || out.Denials[0].Target != "echo hello > shell.txt" || !strings.HasPrefix(out.Denials[0].Reason, "Permission to use Bash") {
		t.Errorf("Denials = %+v", out.Denials)
	}
	if !out.Finished || !out.Success {
		t.Errorf("Outcome = %+v", out)
	}
}

func TestClaudeParserDenialOrderAndTargets(t *testing.T) {
	long := strings.Repeat("x", 300)
	out := parse(lines(
		use("1", "Bash", `{"command":"ls"}`),
		use("2", "Edit", `{"file_path":"/a/b.go","old_string":"x"}`),
		use("3", "Write", `{"file_path":"/a/c.go","content":"y"}`),
		use("4", "NotebookEdit", `{"notebook_path":"/n.ipynb"}`),
		use("5", "WebFetch", `{"url":"https://example.com"}`),
		use("6", "Mystery", `{"a":1,"b":"`+long+`"}`),
		denyEvent("3", "Write"), denyEvent("1", "Bash"), denyEvent("2", "Edit"),
		denyEvent("4", "NotebookEdit"), denyEvent("5", "WebFetch"), denyEvent("6", "Mystery"),
	))
	var tools, targets []string
	for _, d := range out.Denials {
		tools = append(tools, d.Tool)
		targets = append(targets, d.Target)
	}
	if want := []string{"Write", "Bash", "Edit", "NotebookEdit", "WebFetch", "Mystery"}; !reflect.DeepEqual(tools, want) {
		t.Errorf("tools = %v, want %v", tools, want)
	}
	compact := `{"a":1,"b":"` + long
	want := []string{"/a/c.go", "ls", "/a/b.go", "/n.ipynb", "https://example.com", compact[:200] + "…"}
	if !reflect.DeepEqual(targets, want) {
		t.Errorf("targets = %q, want %q", targets, want)
	}
	if out.Denials[0].Shell || !out.Denials[1].Shell {
		t.Errorf("Shell flags wrong: %+v", out.Denials[:2])
	}
}

func TestClaudeParserDenialUnknownInputHasEmptyTarget(t *testing.T) {
	out := parse(denyEvent("nope", "Bash"))
	if len(out.Denials) != 1 || out.Denials[0].Target != "" || !out.Denials[0].Shell {
		t.Errorf("Denials = %+v", out.Denials)
	}
}

func TestClaudeParserNoDenialsIsEmptySlice(t *testing.T) {
	for _, s := range []string{"", fixture(t, "success.jsonl")} {
		if out := parse(s); out.Denials == nil || len(out.Denials) != 0 {
			t.Errorf("Denials = %#v", out.Denials)
		}
	}
}

func TestClaudeParserDenialInTruncatedStream(t *testing.T) {
	out := parse(lines(use("t1", "Bash", `{"command":"ls"}`), denyEvent("t1", "Bash")))
	if out.Finished || len(out.Denials) != 1 || out.Denials[0].Target != "ls" {
		t.Errorf("Outcome = %+v", out)
	}
}

func TestClaudeParserIgnoresOtherSystemEvents(t *testing.T) {
	out := parse(lines(
		`{"type":"system","subtype":"hook_started","tool_name":"Bash","tool_use_id":"t1"}`,
		`{"type":"system","subtype":"status","message":"permission_denied"}`,
		`{"type":"assistant","message":"not an object"}`,
	))
	if len(out.Denials) != 0 {
		t.Errorf("Denials = %+v", out.Denials)
	}
}
