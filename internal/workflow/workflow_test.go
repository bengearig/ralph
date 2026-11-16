package workflow

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAllLoadsStatus(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for i, in := range all {
		if i > 0 && all[i-1].Name >= in.Name {
			t.Errorf("not sorted at %q", in.Name)
		}
		if in.Name == "status" {
			found = true
			if in.Description != "ralph status: summarize the work and suggest what to do next" {
				t.Errorf("description = %q", in.Description)
			}
			if in.ArgumentHint != "[unit ID, for example B-1, S-2 or T-3]" {
				t.Errorf("argument hint = %q", in.ArgumentHint)
			}
		}
	}
	if !found {
		t.Fatal("status instruction missing")
	}
	got, ok, err := Get("status")
	if err != nil || !ok || got.Name != "status" {
		t.Errorf("Get(status) = %v, %v, %v", got, ok, err)
	}
	if _, ok, err := Get("nope"); ok || err != nil {
		t.Errorf("Get(nope) = %v, %v", ok, err)
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
