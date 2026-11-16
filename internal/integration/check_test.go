package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/project"
)

type fakeAdapter struct {
	name  string
	arts  []harness.Artifact
	calls int
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Detect(context.Context, string) harness.Detection {
	return harness.Detection{Found: true}
}
func (f *fakeAdapter) Artifacts(harness.Inputs) ([]harness.Artifact, error) {
	f.calls++
	return f.arts, nil
}

func check(t *testing.T, root string, adapters ...harness.Adapter) []Outdated {
	t.Helper()
	out, err := Check(context.Background(), root, adapters)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func paths(out []Outdated) []string {
	var p []string
	for _, o := range out {
		p = append(p, o.Path)
	}
	return p
}

func installed(t *testing.T, arts ...harness.Artifact) (string, *fakeAdapter) {
	t.Helper()
	root := t.TempDir()
	apply(t, root, arts...)
	return root, &fakeAdapter{name: "fake", arts: arts}
}

func TestCheckNothingWithoutManifest(t *testing.T) {
	f := &fakeAdapter{name: "fake", arts: []harness.Artifact{file("a.md", "x")}}
	if out := check(t, t.TempDir(), f); len(out) != 0 || f.calls != 0 {
		t.Fatalf("out = %+v, calls = %d", out, f.calls)
	}
}

func TestCheckUpToDate(t *testing.T) {
	root, f := installed(t, file("a.md", "x"), section("CLAUDE.md", "body"), entry(".mcp.json", `{"a":1}`))
	if out := check(t, root, f); len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckOutdatedFile(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"))
	f.arts = []harness.Artifact{file("a.md", "v2")}
	out := check(t, root, f)
	if len(out) != 1 || out[0].Path != "a.md" || out[0].ID != "file:a.md" || out[0].Label != "file a.md" {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckOutdatedSectionAndJSON(t *testing.T) {
	root, f := installed(t, section("CLAUDE.md", "v1"), entry(".mcp.json", `{"a":1}`))
	f.arts = []harness.Artifact{section("CLAUDE.md", "v2"), entry(".mcp.json", `{"a":2}`)}
	if got, want := paths(check(t, root, f)), []string{".mcp.json", "CLAUDE.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestCheckNotReportedWhenEdited(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"), section("CLAUDE.md", "v1"))
	write1(t, root, "a.md", "mine")
	write1(t, root, "CLAUDE.md", "<!-- ralph:begin -->\nmine\n<!-- ralph:end -->\n")
	f.arts = []harness.Artifact{file("a.md", "v2"), section("CLAUDE.md", "v2")}
	if out := check(t, root, f); len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckNotReportedWhenMissing(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"), section("CLAUDE.md", "v1"))
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	write1(t, root, "CLAUDE.md", "no section\n")
	f.arts = []harness.Artifact{file("a.md", "v2"), section("CLAUDE.md", "v2")}
	if out := check(t, root, f); len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckNotReportedWhenCustomized(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"), file("b.md", "v1"))
	path := filepath.Join(root, project.DirName, project.IntegrationFile)
	m := manifest(t, root, project.IntegrationFile)
	m.Customized = []string{"a.md", "file:b.md"}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	f.arts = []harness.Artifact{file("a.md", "v2"), file("b.md", "v2")}
	if out := check(t, root, f); len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckNotReportedWhenUnrecorded(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"))
	write1(t, root, "b.md", "theirs")
	f.arts = []harness.Artifact{file("a.md", "v1"), file("b.md", "v2"), file("c.md", "v2")}
	if out := check(t, root, f); len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCheckSortedByPath(t *testing.T) {
	root, f := installed(t, file("z.md", "1"), file("m.md", "1"), file("a.md", "1"),
		item(".claude/settings.local.json", `"x"`, harness.ScopeLocal))
	f.arts = []harness.Artifact{file("z.md", "2"), file("m.md", "2"), file("a.md", "2"),
		item(".claude/settings.local.json", `"x"`, harness.ScopeLocal)}
	if got, want := paths(check(t, root, f)), []string{"a.md", "m.md", "z.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestCheckOnlyAdaptersWithEntries(t *testing.T) {
	root, f := installed(t, file("a.md", "v1"))
	other := &fakeAdapter{name: "other", arts: []harness.Artifact{file("o.md", "x")}}
	check(t, root, f, other)
	if f.calls != 1 || other.calls != 0 {
		t.Fatalf("calls = %d, %d", f.calls, other.calls)
	}
}

func TestCheckErrorNamesFile(t *testing.T) {
	for _, name := range []string{project.IntegrationFile, project.IntegrationLocalFile} {
		root := t.TempDir()
		write1(t, root, filepath.ToSlash(filepath.Join(project.DirName, name)), "{not json")
		_, err := Check(context.Background(), root, []harness.Adapter{&fakeAdapter{name: "fake"}})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCheckErrorFromAdapter(t *testing.T) {
	root, _ := installed(t, file("a.md", "v1"))
	_, err := Check(context.Background(), root, []harness.Adapter{&errAdapter{}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

type errAdapter struct{ fakeAdapter }

func (*errAdapter) Name() string { return "fake" }
func (*errAdapter) Artifacts(harness.Inputs) ([]harness.Artifact, error) {
	return nil, errors.New("boom")
}
