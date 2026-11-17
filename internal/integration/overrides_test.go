package integration

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/workflow"
)

func writeWorkflow(t *testing.T, root, name, content string) {
	t.Helper()
	write1(t, root, filepath.ToSlash(filepath.Join(project.DirName, "workflow", name)), content)
}

func TestInputsForUsesOverrides(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "quick.md", "my quick\n")
	writeWorkflow(t, root, "status.append.md", "extra\n")
	in, overrides, err := InputsWithOverrides(root)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, i := range in.Instructions {
		bodies[i.Name] = i.Body
	}
	if bodies["quick"] != "my quick\n" || !strings.HasSuffix(bodies["status"], "\n\nextra\n") {
		t.Errorf("bodies = %q", bodies)
	}
	want := []workflow.Override{
		{Name: "quick", File: "quick.md", Kind: "replace"},
		{Name: "status", File: "status.append.md", Kind: "append"},
	}
	if !reflect.DeepEqual(overrides, want) {
		t.Errorf("overrides = %v", overrides)
	}
	plain, err := InputsFor(root)
	if err != nil || !reflect.DeepEqual(plain.Instructions, in.Instructions) {
		t.Errorf("InputsFor differs: %v", err)
	}
}

func TestInputsForOverrideErrorNamesFile(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "nope.md", "text\n")
	for name, fn := range map[string]func() error{
		"InputsFor":           func() error { _, err := InputsFor(root); return err },
		"InputsWithOverrides": func() error { _, _, err := InputsWithOverrides(root); return err },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "nope.md") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

type commandAdapter struct{ root string }

func (commandAdapter) Name() string { return "fake" }
func (commandAdapter) Detect(context.Context, string) harness.Detection {
	return harness.Detection{Found: true}
}
func (a commandAdapter) Artifacts(in harness.Inputs) ([]harness.Artifact, error) {
	var arts []harness.Artifact
	for _, i := range in.Instructions {
		arts = append(arts, file("commands/"+i.Name+".md", i.Body))
	}
	return arts, nil
}

func TestOutOfDateAfterOverrideEdit(t *testing.T) {
	root := t.TempDir()
	ad := commandAdapter{root}
	applyAll := func() {
		t.Helper()
		in, err := InputsFor(root)
		if err != nil {
			t.Fatal(err)
		}
		arts, _ := ad.Artifacts(in)
		apply(t, root, arts...)
	}
	writeWorkflow(t, root, "quick.append.md", "first\n")
	applyAll()
	if got := check(t, root, ad); len(got) != 0 {
		t.Fatalf("outdated before edit: %v", got)
	}
	writeWorkflow(t, root, "quick.append.md", "second\n")
	if got := paths(check(t, root, ad)); !reflect.DeepEqual(got, []string{"commands/quick.md"}) {
		t.Fatalf("outdated = %v", got)
	}
	applyAll()
	if got := check(t, root, ad); len(got) != 0 {
		t.Errorf("outdated after apply: %v", got)
	}
}

func (commandAdapter) Headless(harness.HeadlessRequest) (harness.Invocation, error) {
	return harness.Invocation{}, nil
}
func (commandAdapter) NewParser() harness.Parser          { return nil }
func (commandAdapter) Capabilities() harness.Capabilities { return harness.Capabilities{} }
