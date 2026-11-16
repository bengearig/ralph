package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeOverride(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func builtIn(t *testing.T, name string) Instruction {
	t.Helper()
	in, ok, err := Get(name)
	if err != nil || !ok {
		t.Fatal(name, ok, err)
	}
	return in
}

func loaded(t *testing.T, dir, name string) Instruction {
	t.Helper()
	all, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range all {
		if in.Name == name {
			return in
		}
	}
	t.Fatalf("no instruction %s", name)
	return Instruction{}
}

func loadErr(t *testing.T, dir string) string {
	t.Helper()
	_, _, err := Load(dir)
	if err == nil {
		t.Fatal("Load succeeded")
	}
	return err.Error()
}

func TestLoadNoDirectory(t *testing.T) {
	got, overrides, err := Load(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := All()
	if !reflect.DeepEqual(got, want) || len(overrides) != 0 {
		t.Errorf("got %v, %v", got, overrides)
	}
}

func TestLoadReplaceBody(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "quick.md", "Do it my way: {{tool \"quick_start\"}}\n")
	got, base := loaded(t, dir, "quick"), builtIn(t, "quick")
	if got.Body != "Do it my way: {{tool \"quick_start\"}}\n" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Description != base.Description || got.ArgumentHint != base.ArgumentHint {
		t.Errorf("got %q, %q", got.Description, got.ArgumentHint)
	}
}

func TestLoadReplaceWithFrontMatter(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "quick.md", "---\ndescription: my quick\nargument-hint: \"[my hint]\"\n---\nbody\n")
	writeOverride(t, dir, "status.md", "---\ndescription: my status\n---\nbody\n")
	quick := loaded(t, dir, "quick")
	if quick.Description != "my quick" || quick.ArgumentHint != "[my hint]" || quick.Body != "body\n" {
		t.Errorf("quick = %+v", quick)
	}
	status, base := loaded(t, dir, "status"), builtIn(t, "status")
	if status.Description != "my status" || status.ArgumentHint != base.ArgumentHint {
		t.Errorf("status = %+v", status)
	}
}

func TestLoadAppend(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "status.append.md", "Also do this.\n")
	got, base := loaded(t, dir, "status"), builtIn(t, "status")
	if want := strings.TrimRight(base.Body, "\n") + "\n\nAlso do this.\n"; got.Body != want {
		t.Errorf("body = %q, want %q", got.Body, want)
	}
	if got.Description != base.Description {
		t.Errorf("description = %q", got.Description)
	}
}

func TestLoadReplaceAndAppendTogether(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "status.md", "replacement\n")
	writeOverride(t, dir, "status.append.md", "addition")
	if got := loaded(t, dir, "status").Body; got != "replacement\n\naddition\n" {
		t.Errorf("body = %q", got)
	}
}

func TestLoadOverridesListedInOrder(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"status.append.md", "receive.md", "status.md", "quick.append.md"} {
		writeOverride(t, dir, f, "text\n")
	}
	_, got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Override{
		{Name: "quick", File: "quick.append.md", Kind: "append"},
		{Name: "receive", File: "receive.md", Kind: "replace"},
		{Name: "status", File: "status.md", Kind: "replace"},
		{Name: "status", File: "status.append.md", Kind: "append"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoadUnknownInstructionName(t *testing.T) {
	for file, want := range map[string]string{
		"nope.md":        "nope.md: no built-in instruction named nope",
		"nope.append.md": "nope.append.md: no built-in instruction named nope",
	} {
		dir := t.TempDir()
		writeOverride(t, dir, file, "text\n")
		if got := loadErr(t, dir); got != want {
			t.Errorf("%s: err = %q, want %q", file, got, want)
		}
	}
}

func TestLoadAppendRejectsFrontMatter(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "quick.append.md", "---\ndescription: x\n---\ntext\n")
	if got := loadErr(t, dir); !strings.Contains(got, "quick.append.md") || !strings.Contains(got, "front matter") {
		t.Errorf("err = %q", got)
	}
}

func TestLoadEmptyOverrideRejected(t *testing.T) {
	for _, file := range []string{"quick.md", "quick.append.md"} {
		for _, content := range []string{"", " \n\n"} {
			dir := t.TempDir()
			writeOverride(t, dir, file, content)
			if got := loadErr(t, dir); !strings.Contains(got, file) {
				t.Errorf("%s %q: err = %q", file, content, got)
			}
		}
	}
	dir := t.TempDir()
	writeOverride(t, dir, "quick.md", "---\ndescription: only front matter\n---\n")
	if got := loadErr(t, dir); !strings.Contains(got, "quick.md") {
		t.Errorf("err = %q", got)
	}
}

func TestLoadBadTemplateNamesFile(t *testing.T) {
	for _, file := range []string{"quick.md", "quick.append.md"} {
		dir := t.TempDir()
		writeOverride(t, dir, file, "broken {{tool \"x\"\n")
		if got := loadErr(t, dir); !strings.Contains(got, file) {
			t.Errorf("%s: err = %q", file, got)
		}
	}
}

func TestLoadDisallowedFunctionNamesFile(t *testing.T) {
	for _, file := range []string{"quick.md", "quick.append.md"} {
		dir := t.TempDir()
		writeOverride(t, dir, file, "{{printf \"x\"}}\n")
		if got := loadErr(t, dir); !strings.Contains(got, file) || !strings.Contains(got, "not allowed") {
			t.Errorf("%s: err = %q", file, got)
		}
	}
}

func TestLoadIgnoresOtherFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "README.txt", "notes")
	writeOverride(t, dir, ".hidden.md", "text")
	writeOverride(t, dir, ".quick.md", "text")
	if err := os.Mkdir(filepath.Join(dir, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, overrides, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := All()
	if !reflect.DeepEqual(got, want) || len(overrides) != 0 {
		t.Errorf("got %v, %v", got, overrides)
	}
}

func TestLoadReplacementMayUseHarnessText(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "quick.md", "Before.\n{{harnessText \"approval\"}}\n")
	out, err := loaded(t, dir, "quick").Render(RenderContext{Tool: func(n string) string { return n }, Text: map[string]string{"approval": "ASK FIRST"}})
	if err != nil || out != "Before.\nASK FIRST\n" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestLoadDoesNotChangeAll(t *testing.T) {
	before, _ := All()
	dir := t.TempDir()
	writeOverride(t, dir, "quick.md", "replaced\n")
	writeOverride(t, dir, "status.append.md", "added\n")
	if _, _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := All()
	if !reflect.DeepEqual(before, after) {
		t.Error("All() changed")
	}
}
