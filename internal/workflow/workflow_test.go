package workflow

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAllLoadsArticulateReceiveStatusAndQuick(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for i, in := range all {
		if i > 0 && all[i-1].Name >= in.Name {
			t.Errorf("not sorted at %q", in.Name)
		}
		names = append(names, in.Name)
		if in.Name == "status" {
			if in.Description != "ralph status: summarize the work and suggest what to do next" {
				t.Errorf("description = %q", in.Description)
			}
			if in.ArgumentHint != "[unit ID, for example B-1, S-2 or T-3]" {
				t.Errorf("argument hint = %q", in.ArgumentHint)
			}
		}
	}
	if want := []string{"articulate", "quick", "receive", "status"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	got, ok, err := Get("status")
	if err != nil || !ok || got.Name != "status" {
		t.Errorf("Get(status) = %v, %v, %v", got, ok, err)
	}
	if _, ok, err := Get("nope"); ok || err != nil {
		t.Errorf("Get(nope) = %v, %v", ok, err)
	}
}

func quickInstruction(t *testing.T) Instruction {
	t.Helper()
	in, ok, err := Get("quick")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return in
}

func TestQuickInstructionFrontMatter(t *testing.T) {
	in := quickInstruction(t)
	if want := "ralph quick: do a small piece of work on its own branch and open a pull request"; in.Description != want {
		t.Errorf("description = %q", in.Description)
	}
	if in.ArgumentHint != "[what to do]" {
		t.Errorf("argument hint = %q", in.ArgumentHint)
	}
}

func TestQuickInstructionToolsReferenced(t *testing.T) {
	if got, want := quickInstruction(t).Tools(), []string{"quick_finish", "quick_start"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
}

func TestQuickInstructionContent(t *testing.T) {
	out, err := quickInstruction(t).Render(RenderContext{Tool: func(n string) string { return n }, Arguments: "ARGS"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"only where the request is ambiguous",
		"much larger than a small change",
		"stop without starting anything",
		"verification_failed",
		"Never merge",
		"Notice for the user:",
		".ralph",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered text lacks %q", want)
		}
	}
}

func TestQuickInstructionHarnessNeutral(t *testing.T) {
	body := quickInstruction(t).Body
	for _, bad := range []string{"mcp__", "$ARGUMENTS", "CLAUDE"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains harness-specific %q", bad)
		}
	}
}

func TestQuickInstructionRendersArguments(t *testing.T) {
	out, err := quickInstruction(t).Render(RenderContext{Tool: func(n string) string { return n }, Arguments: "FAKE-ARGUMENTS-VALUE"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FAKE-ARGUMENTS-VALUE") {
		t.Errorf("arguments missing from %q", out)
	}
}

func TestStatusInstructionMentionsUnfinalized(t *testing.T) {
	in, ok, err := Get("status")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !strings.Contains(in.Body, "unfinalized") || !strings.Contains(in.Body, "branch") {
		t.Errorf("body does not tell the agent to report unfinalized quick work with its branch:\n%s", in.Body)
	}
}

func TestInstructionNamesMatchFiles(t *testing.T) {
	entries, err := instructionFiles.ReadDir("instructions")
	if err != nil {
		t.Fatal(err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(entries) {
		t.Fatalf("%d instructions for %d files", len(all), len(entries))
	}
	for _, in := range all {
		if !namePattern.MatchString(in.Name) {
			t.Errorf("name %q is invalid", in.Name)
		}
		if _, err := instructionFiles.ReadFile("instructions/" + in.Name + ".md"); err != nil {
			t.Errorf("no file for %q: %v", in.Name, err)
		}
	}
}

func TestFrontMatterErrors(t *testing.T) {
	tests := []struct {
		name, file, content, want string
	}{
		{"missing description", "a.md", "---\nargument-hint: x\n---\nbody\n", "no description"},
		{"bad yaml", "a.md", "---\ndescription: [oops\n---\nbody\n", "front matter"},
		{"no front matter", "a.md", "just a body\n", "missing front matter"},
		{"unclosed", "a.md", "---\ndescription: x\nbody\n", "not closed"},
		{"name does not match", "Bad_Name.md", "---\ndescription: x\n---\nbody\n", "must match"},
		{"bad template", "a.md", "---\ndescription: x\n---\n{{nope}}\n", "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{"instructions/" + tt.file: {Data: []byte(tt.content)}}
			_, err := load(fsys, "instructions")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), tt.file) {
				t.Errorf("error %q should name %q and contain %q", err, tt.file, tt.want)
			}
		})
	}
}

func TestRenderToolAndArguments(t *testing.T) {
	in := Instruction{Name: "x", Body: `call {{tool "a"}} then {{tool "b"}} with {{arguments}}`}
	got, err := in.Render(RenderContext{Tool: func(n string) string { return "T(" + n + ")" }, Arguments: "ARGS"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "call T(a) then T(b) with ARGS"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderRejectsOtherFunctions(t *testing.T) {
	rc := RenderContext{Tool: func(n string) string { return n }}
	for _, body := range []string{`{{upper "x"}}`, `{{printf "%d" 1}}`, `{{.Field}}`, `{{tool}}`} {
		in := Instruction{Name: "bad", Body: body}
		_, err := in.Render(rc)
		if err == nil || !strings.Contains(err.Error(), "bad") {
			t.Errorf("body %q: error = %v, want one naming the instruction", body, err)
		}
	}
	if _, err := (Instruction{Name: "bad", Body: "x"}).Render(RenderContext{}); err == nil {
		t.Error("nil Tool should be an error")
	}
}

func TestToolsListsReferencedTools(t *testing.T) {
	in := Instruction{Name: "x", Body: `{{tool "b"}} {{tool "a"}} {{tool "b"}}`}
	if got, want := in.Tools(), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
	status, ok, err := Get("status")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got, want := status.Tools(), []string{"status", "unit_get"}; !reflect.DeepEqual(got, want) {
		t.Errorf("status Tools() = %v, want %v", got, want)
	}
}

func TestStatusInstructionContent(t *testing.T) {
	in, ok, err := Get("status")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !strings.Contains(in.Body, "Notice for the user:") {
		t.Error("body does not mention the notice prefix")
	}
	if !strings.Contains(in.Body, "Change nothing") {
		t.Error("body does not say it changes nothing")
	}
	out, err := in.Render(RenderContext{Tool: func(n string) string { return n }, Arguments: "ARGS"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"mcp__", "$ARGUMENTS"} {
		if strings.Contains(in.Body, bad) || strings.Contains(out, bad) {
			t.Errorf("instruction contains harness-specific %q", bad)
		}
	}
}

func neutralContext() RenderContext {
	return RenderContext{Tool: func(n string) string { return n }}
}

func TestHarnessTextDefault(t *testing.T) {
	out, err := Instruction{Name: "x", Body: `{{harnessText "approval"}}`}.Render(neutralContext())
	if err != nil {
		t.Fatal(err)
	}
	if out != defaultText["approval"] || !strings.HasPrefix(out, "Show the outline to the user in this conversation and wait.") {
		t.Errorf("got %q", out)
	}
}

func TestHarnessTextAdapterWins(t *testing.T) {
	rc := neutralContext()
	rc.Text = map[string]string{"approval": "ADAPTER"}
	out, err := Instruction{Name: "x", Body: `{{harnessText "approval"}}`}.Render(rc)
	if err != nil || out != "ADAPTER" {
		t.Errorf("got %q, %v", out, err)
	}
}

func TestHarnessTextUnknownKeyErrors(t *testing.T) {
	_, err := Instruction{Name: "myinstr", Body: `{{harnessText "nope"}}`}.Render(neutralContext())
	if err == nil || !strings.Contains(err.Error(), "myinstr") || !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want one naming the instruction and the key", err)
	}
}

func TestHarnessTextNeedsLiteralArgument(t *testing.T) {
	for _, body := range []string{`{{harnessText .Key}}`, `{{harnessText "a" "b"}}`, `{{harnessText}}`, `{{$k := "approval"}}{{harnessText $k}}`} {
		if _, err := (Instruction{Name: "bad", Body: body}).Render(neutralContext()); err == nil {
			t.Errorf("body %q was accepted", body)
		}
	}
}

func TestToolsIgnoresHarnessText(t *testing.T) {
	in := Instruction{Name: "x", Body: `{{tool "b"}} {{harnessText "approval"}} {{tool "a"}}`}
	if got, want := in.Tools(), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
}

func receiveInstruction(t *testing.T) Instruction {
	t.Helper()
	in, ok, err := Get("receive")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return in
}

func TestReceiveFrontMatter(t *testing.T) {
	in := receiveInstruction(t)
	if want := "ralph receive: capture instructions and plan them at a high level"; in.Description != want {
		t.Errorf("description = %q", in.Description)
	}
	if want := "[the instructions, or the ID of a brief to revise]"; in.ArgumentHint != want {
		t.Errorf("argument hint = %q", in.ArgumentHint)
	}
}

func TestReceiveToolsReferenced(t *testing.T) {
	want := []string{"dependency_add", "note_add", "planning_settings", "status", "task_set_state", "unit_create", "unit_get", "unit_list", "unit_update"}
	if got := receiveInstruction(t).Tools(); !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
}

func TestReceiveContent(t *testing.T) {
	out, err := receiveInstruction(t).Render(neutralContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"If nothing was typed, ask the user",
		"planning_settings",
		"word for word",
		"a few questions at a time",
		"Prefer one slice over two that could not stand alone",
		"dependency_add",
		"Order alone is not a dependency",
		"when the outline revises an existing one",
		"Always get the user's approval before applying a revision",
		"Never delete anything",
		"CANCELLED with a reason",
		"move it back to PROPOSED with a kickback reason",
		"Notice for the user:",
		"does not work out task details, write acceptance criteria, promote any task, or write code",
		defaultText["approval"],
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered text lacks %q", want)
		}
	}
}

func TestReceiveHarnessNeutralBody(t *testing.T) {
	body := receiveInstruction(t).Body
	for _, bad := range []string{"mcp__", "$ARGUMENTS", "CLAUDE", "EnterPlanMode", "ExitPlanMode", "AGENTS"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains harness-specific %q", bad)
		}
	}
}

func TestReceiveNamesNoLoopOrDocumentTools(t *testing.T) {
	in := receiveInstruction(t)
	for _, tool := range in.Tools() {
		if strings.HasPrefix(tool, "doc_") || strings.Contains(tool, "loop") {
			t.Errorf("references tool %q", tool)
		}
	}
	for _, bad := range []string{"doc_", "ralph loop", "document"} {
		if strings.Contains(in.Body, bad) {
			t.Errorf("body mentions %q", bad)
		}
	}
}

func articulateInstruction(t *testing.T) Instruction {
	t.Helper()
	in, ok, err := Get("articulate")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return in
}

func TestArticulateFrontMatter(t *testing.T) {
	in := articulateInstruction(t)
	if want := "ralph articulate: work out each task's details with the user and promote it"; in.Description != want {
		t.Errorf("description = %q", in.Description)
	}
	if want := "[a brief, slice or task ID]"; in.ArgumentHint != want {
		t.Errorf("argument hint = %q", in.ArgumentHint)
	}
}

func TestArticulateToolsReferenced(t *testing.T) {
	want := []string{"dependency_add", "note_add", "status", "task_set_state", "unit_create", "unit_get", "unit_update"}
	if got := articulateInstruction(t).Tools(); !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
}

func TestArticulateContent(t *testing.T) {
	out, err := articulateInstruction(t).Render(neutralContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ask the user which brief, slice or task to articulate",
		"the notes recorded on it during receive",
		"one task at a time, in slice order and then position order",
		"a few questions at a time",
		"a stranger with no access to this conversation could implement from",
		"verifiable, preferably by tests or commands",
		"names the narrowest command that proves the task",
		"only on a slice's last task or in its final check",
		"set TODO with a short reason",
		"without asking for separate permission",
		"refused as not ready",
		"fill in what the error lists and promote the task again",
		"KICKBACK note",
		"suggest planning the brief again with the receive command and the brief's ID",
		"Leave TODO tasks alone unless the user wants to revisit one",
		"Notice for the user:",
		"writes no production code",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered text lacks %q", want)
		}
	}
}

func TestArticulateHarnessNeutralBody(t *testing.T) {
	body := articulateInstruction(t).Body
	for _, bad := range []string{"mcp__", "$ARGUMENTS", "CLAUDE", "AGENTS", "EnterPlanMode", "ExitPlanMode"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains harness-specific %q", bad)
		}
	}
}

func TestArticulateNamesNoLoopCommand(t *testing.T) {
	in := articulateInstruction(t)
	for _, tool := range in.Tools() {
		if strings.Contains(tool, "loop") {
			t.Errorf("references tool %q", tool)
		}
	}
	if strings.Contains(strings.ToLower(in.Body), "loop") {
		t.Error("body names a loop command")
	}
}
