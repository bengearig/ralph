package harness

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

type projectReader struct{}

func (projectReader) Name() string                             { return "reader" }
func (projectReader) Detect(context.Context, string) Detection { return Detection{} }
func (projectReader) Artifacts(in Inputs) ([]Artifact, error) {
	path := "NOTES.md"
	if in.Project != nil {
		if _, err := fs.Stat(in.Project, "AGENTS.md"); err == nil {
			path = "AGENTS.md"
		}
	}
	return []Artifact{{Kind: KindSection, Scope: ScopeProject, Path: path, Content: []byte("x")}}, nil
}

func TestInputsProjectNilIsEmpty(t *testing.T) {
	var a Adapter = projectReader{}
	nilArts, err := a.Artifacts(Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	emptyArts, err := a.Artifacts(Inputs{Project: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	if nilArts[0].Path != "NOTES.md" || emptyArts[0].Path != nilArts[0].Path {
		t.Errorf("nil = %q, empty = %q", nilArts[0].Path, emptyArts[0].Path)
	}
	full, err := a.Artifacts(Inputs{Project: fstest.MapFS{"AGENTS.md": {Data: []byte("a")}}})
	if err != nil {
		t.Fatal(err)
	}
	if full[0].Path != "AGENTS.md" {
		t.Errorf("path = %q", full[0].Path)
	}
}

func TestDetectionUnsupportedField(t *testing.T) {
	if (Detection{Found: true}).Unsupported != "" {
		t.Error("Unsupported must default to empty")
	}
	d := Detection{Found: true, Unsupported: "too old"}
	if d.Unsupported != "too old" {
		t.Errorf("Unsupported = %q", d.Unsupported)
	}
}

func (projectReader) Headless(HeadlessRequest) (Invocation, error) {
	return Invocation{}, nil
}
func (projectReader) NewParser() Parser          { return nil }
func (projectReader) Capabilities() Capabilities { return Capabilities{} }
