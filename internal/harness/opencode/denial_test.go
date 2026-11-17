package opencode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func toolEvent(id, tool, status, input, errText string) string {
	return `{"type":"tool_use","sessionID":"s","part":{"id":"` + id + `","type":"tool","tool":"` + tool + `","state":{"status":"` + status + `","input":` + input + `,"error":"` + errText + `"}}}`
}

func TestOpenCodeParserDenialShell(t *testing.T) {
	out := parse(fixture(t, "denied.jsonl"))
	want := []harness.Denial{{Tool: "shell", Target: "echo hello > shell.txt", Reason: "Permission denied: shell", Shell: true}}
	if !reflect.DeepEqual(out.Denials, want) {
		t.Errorf("Denials = %+v, want %+v", out.Denials, want)
	}
}

func TestOpenCodeParserDenialEditUsesPath(t *testing.T) {
	out := parse(strings.Join([]string{
		toolEvent("1", "edit", "error", `{"filePath":"/x/a.go"}`, "Permission denied: edit"),
		toolEvent("2", "write", "error", `{"path":"b.txt","file_path":"z"}`, "Permission denied: write"),
		toolEvent("3", "patch", "error", `{"file_path":"c.txt"}`, "Permission denied: patch"),
		toolEvent("4", "webfetch", "error", `{"url":"http://x"}`, "Permission denied: webfetch"),
	}, "\n"))
	var got []string
	for _, d := range out.Denials {
		got = append(got, d.Target)
		if d.Shell {
			t.Errorf("%s is not a shell tool", d.Tool)
		}
	}
	if want := []string{"/x/a.go", "b.txt", "c.txt", `{"url":"http://x"}`}; !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %q, want %q", got, want)
	}
}

func TestOpenCodeParserDenialRejectedWording(t *testing.T) {
	out := parse(toolEvent("1", "bash", "error", `{"command":"ls"}`, "permission REJECTED: bash"))
	if len(out.Denials) != 1 || !out.Denials[0].Shell || out.Denials[0].Target != "ls" {
		t.Errorf("Denials = %+v", out.Denials)
	}
}

func TestOpenCodeParserOtherToolErrorIsNotDenial(t *testing.T) {
	out := parse(strings.Join([]string{
		toolEvent("1", "read", "error", `{"path":"nope"}`, "ENOENT: no such file"),
		toolEvent("2", "shell", "error", `{"command":"x"}`, "Error: permission denied while opening"),
	}, "\n"))
	if len(out.Denials) != 0 {
		t.Errorf("Denials = %+v", out.Denials)
	}
}

func TestOpenCodeParserCompletedToolIsNotDenial(t *testing.T) {
	out := parse(toolEvent("1", "shell", "completed", `{"command":"ls"}`, "Permission denied: shell"))
	if len(out.Denials) != 0 {
		t.Errorf("Denials = %+v", out.Denials)
	}
}

func TestOpenCodeParserSameCallCountedOnce(t *testing.T) {
	e := toolEvent("p1", "shell", "error", `{"command":"ls"}`, "Permission denied: shell")
	out := parse(e + "\n" + e + "\n" + toolEvent("p2", "shell", "error", `{"command":"ls"}`, "Permission denied: shell"))
	if len(out.Denials) != 2 {
		t.Errorf("Denials = %+v, want two", out.Denials)
	}
	byPartID := `{"type":"tool_use","part":{"partID":"q","tool":"shell","state":{"status":"error","input":{"command":"ls"},"error":"Permission denied: shell"}}}`
	if out := parse(byPartID + "\n" + byPartID); len(out.Denials) != 1 {
		t.Errorf("partID Denials = %+v", out.Denials)
	}
}

func TestOpenCodeParserNoDenialsIsEmptySlice(t *testing.T) {
	for _, s := range []string{"", fixture(t, "success.jsonl")} {
		if out := parse(s); out.Denials == nil || len(out.Denials) != 0 {
			t.Errorf("Denials = %#v", out.Denials)
		}
	}
}
