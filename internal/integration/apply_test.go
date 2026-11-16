package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/project"
)

const ver = "test"

func file(path, content string) harness.Artifact {
	return harness.Artifact{Kind: harness.KindFile, Scope: harness.ScopeProject, Path: path, Content: []byte(content), Label: "file " + path}
}

func section(path, body string) harness.Artifact {
	return harness.Artifact{Kind: harness.KindSection, Scope: harness.ScopeProject, Path: path, Content: []byte(body)}
}

func entry(path, value string) harness.Artifact {
	return harness.Artifact{Kind: harness.KindJSONEntry, Scope: harness.ScopeProject, Path: path, KeyPath: []string{"mcpServers", "ralph"}, Content: []byte(value)}
}

func item(path, value string, scope harness.Scope) harness.Artifact {
	return harness.Artifact{Kind: harness.KindJSONArrayItem, Scope: scope, Path: path, KeyPath: []string{"permissions", "allow"}, Content: []byte(value)}
}

func apply(t *testing.T, root string, arts ...harness.Artifact) []Result {
	t.Helper()
	res, err := Apply(context.Background(), root, "fake", arts, ver)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func write1(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func want(t *testing.T, res []Result, i int, a Action) Result {
	t.Helper()
	if i >= len(res) || res[i].Action != a {
		t.Fatalf("results = %+v, want result %d to be %s", res, i, a)
	}
	return res[i]
}

func manifest(t *testing.T, root, name string) Manifest {
	t.Helper()
	m, err := LoadManifest(filepath.Join(root, project.DirName, name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestApplyInstallsFile(t *testing.T) {
	root := t.TempDir()
	want(t, apply(t, root, file("a/b.md", "hello\n")), 0, ActionInstalled)
	if read(t, root, "a/b.md") != "hello\n" {
		t.Fatal("content")
	}
	if info, _ := os.Stat(filepath.Join(root, "a/b.md")); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", info.Mode())
	}
	m := manifest(t, root, project.IntegrationFile)
	if len(m.Entries) != 1 || m.Entries[0].ID != "file:a/b.md" || m.Entries[0].Adapter != "fake" || m.Entries[0].Ralph != ver {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestApplyUnchangedOnRepeat(t *testing.T) {
	root := t.TempDir()
	arts := []harness.Artifact{
		file("a.md", "x"), section("CLAUDE.md", "body"), entry(".mcp.json", `{"command":"ralph"}`),
		item(".claude/settings.local.json", `"mcp__ralph"`, harness.ScopeLocal),
	}
	apply(t, root, arts...)
	paths := []string{"a.md", "CLAUDE.md", ".mcp.json", ".claude/settings.local.json", ".ralph/integration.json", ".ralph/integration.local.json"}
	old := time.Now().Add(-time.Hour)
	before := map[string]string{}
	for _, p := range paths {
		if err := os.Chtimes(filepath.Join(root, p), old, old); err != nil {
			t.Fatal(err)
		}
		before[p] = read(t, root, p)
	}
	res := apply(t, root, arts...)
	for i := range arts {
		want(t, res, i, ActionUnchanged)
	}
	for _, p := range paths {
		info, _ := os.Stat(filepath.Join(root, p))
		if !info.ModTime().Equal(old) || read(t, root, p) != before[p] {
			t.Errorf("%s was modified", p)
		}
	}
}

func TestApplyUpdatesUntouchedFile(t *testing.T) {
	root := t.TempDir()
	apply(t, root, file("a.md", "v1"))
	want(t, apply(t, root, file("a.md", "v2")), 0, ActionUpdated)
	if read(t, root, "a.md") != "v2" || exists(root, "a.md.new") {
		t.Fatal("not updated cleanly")
	}
	want(t, apply(t, root, file("a.md", "v2")), 0, ActionUnchanged)
}

func TestApplyKeepsEditedFile(t *testing.T) {
	root := t.TempDir()
	apply(t, root, file("a.md", "v1"))
	write1(t, root, "a.md", "mine")
	r := want(t, apply(t, root, file("a.md", "v2")), 0, ActionKept)
	if read(t, root, "a.md") != "mine" || read(t, root, "a.md.new") != "v2" || !strings.Contains(r.Detail, "a.md.new") {
		t.Fatalf("result = %+v", r)
	}
}

func TestApplyKeepsUnrecordedDifferentFile(t *testing.T) {
	root := t.TempDir()
	write1(t, root, "a.md", "older tool")
	want(t, apply(t, root, file("a.md", "new")), 0, ActionKept)
	if read(t, root, "a.md") != "older tool" || read(t, root, "a.md.new") != "new" {
		t.Fatal("wrong files")
	}
	if m := manifest(t, root, project.IntegrationFile); len(m.Entries) != 0 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestApplyAdoptsIdenticalFile(t *testing.T) {
	root := t.TempDir()
	p := write1(t, root, "a.md", "same")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	want(t, apply(t, root, file("a.md", "same")), 0, ActionAdopted)
	if info, _ := os.Stat(p); !info.ModTime().Equal(old) {
		t.Fatal("rewritten")
	}
	if m := manifest(t, root, project.IntegrationFile); len(m.Entries) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	want(t, apply(t, root, file("a.md", "same")), 0, ActionUnchanged)
}

func TestApplyKeepOverwritesOldDotNew(t *testing.T) {
	root := t.TempDir()
	write1(t, root, "a.md", "mine")
	write1(t, root, "a.md.new", "stale")
	want(t, apply(t, root, file("a.md", "fresh")), 0, ActionKept)
	if read(t, root, "a.md.new") != "fresh" {
		t.Fatal("stale .new")
	}
}

func TestApplySectionAppendedToExistingFile(t *testing.T) {
	root := t.TempDir()
	const before = "# Mine\n\nnotes"
	write1(t, root, "CLAUDE.md", before)
	want(t, apply(t, root, section("CLAUDE.md", "ralph body")), 0, ActionInstalled)
	got := read(t, root, "CLAUDE.md")
	if !strings.HasPrefix(got, before) || !strings.Contains(got, BeginMarker+"\nralph body\n"+EndMarker) {
		t.Fatalf("file = %q", got)
	}
}

func TestApplySectionUpdatedWhenUntouched(t *testing.T) {
	root := t.TempDir()
	apply(t, root, section("CLAUDE.md", "v1"))
	want(t, apply(t, root, section("CLAUDE.md", "v2")), 0, ActionUpdated)
	if !strings.Contains(read(t, root, "CLAUDE.md"), "v2") || strings.Contains(read(t, root, "CLAUDE.md"), "v1") {
		t.Fatal("not updated")
	}
}

func TestApplySectionEditedByUserIsKept(t *testing.T) {
	root := t.TempDir()
	apply(t, root, section("CLAUDE.md", "v1"))
	edited := strings.Replace(read(t, root, "CLAUDE.md"), "v1", "mine", 1)
	write1(t, root, "CLAUDE.md", edited)
	want(t, apply(t, root, section("CLAUDE.md", "v2")), 0, ActionKept)
	if read(t, root, "CLAUDE.md") != edited || exists(root, "CLAUDE.md.new") {
		t.Fatal("changed or .new written")
	}
}

func TestApplySectionMalformedMarkersReported(t *testing.T) {
	root := t.TempDir()
	bad := BeginMarker + "\nno end\n"
	write1(t, root, "CLAUDE.md", bad)
	res := apply(t, root, section("CLAUDE.md", "x"), file("a.md", "y"))
	r := want(t, res, 0, ActionKept)
	if r.Detail == "" {
		t.Fatal("no detail")
	}
	want(t, res, 1, ActionInstalled)
	if read(t, root, "CLAUDE.md") != bad {
		t.Fatal("changed")
	}
}

func TestApplyJSONEntryPreservesOtherServers(t *testing.T) {
	root := t.TempDir()
	write1(t, root, ".mcp.json", `{"mcpServers":{"other":{"command":"x"}},"extra":12345678901234567890}`)
	want(t, apply(t, root, entry(".mcp.json", `{"command":"ralph"}`)), 0, ActionInstalled)
	got := read(t, root, ".mcp.json")
	for _, s := range []string{`"other"`, `"ralph"`, "12345678901234567890"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %s in %s", s, got)
		}
	}
}

func TestApplyJSONEntryAdoptsIdentical(t *testing.T) {
	root := t.TempDir()
	orig := "{\n    \"mcpServers\": {\"ralph\": {\"args\": [\"mcp\"], \"command\": \"ralph\"}}\n}"
	write1(t, root, ".mcp.json", orig)
	want(t, apply(t, root, entry(".mcp.json", `{"command":"ralph","args":["mcp"]}`)), 0, ActionAdopted)
	if read(t, root, ".mcp.json") != orig {
		t.Fatal("rewritten")
	}
}

func TestApplyJSONEntryDifferentIsKept(t *testing.T) {
	root := t.TempDir()
	orig := `{"mcpServers":{"ralph":{"command":"other"}}}`
	write1(t, root, ".mcp.json", orig)
	want(t, apply(t, root, entry(".mcp.json", `{"command":"ralph"}`)), 0, ActionKept)
	if read(t, root, ".mcp.json") != orig || exists(root, ".mcp.json.new") {
		t.Fatal("changed")
	}
}

func TestApplyJSONInvalidFileReported(t *testing.T) {
	root := t.TempDir()
	write1(t, root, ".mcp.json", "{broken")
	r := want(t, apply(t, root, entry(".mcp.json", `{"a":1}`)), 0, ActionKept)
	if r.Detail == "" || read(t, root, ".mcp.json") != "{broken" {
		t.Fatalf("result = %+v", r)
	}
}

func TestApplyArrayItemAddedAndAdopted(t *testing.T) {
	root := t.TempDir()
	write1(t, root, "s.json", `{"permissions":{"allow":["Bash"]}}`)
	a := item("s.json", `"mcp__ralph"`, harness.ScopeProject)
	want(t, apply(t, root, a), 0, ActionInstalled)
	if got := read(t, root, "s.json"); !strings.Contains(got, `"Bash"`) || !strings.Contains(got, `"mcp__ralph"`) {
		t.Fatalf("file = %s", got)
	}
	if err := os.Remove(filepath.Join(root, ".ralph", project.IntegrationFile)); err != nil {
		t.Fatal(err)
	}
	want(t, apply(t, root, a), 0, ActionAdopted)
}

func TestApplyLocalScopeUsesLocalManifest(t *testing.T) {
	root := t.TempDir()
	want(t, apply(t, root, item("s.json", `"p"`, harness.ScopeLocal)), 0, ActionInstalled)
	if len(manifest(t, root, project.IntegrationLocalFile).Entries) != 1 {
		t.Fatal("local manifest empty")
	}
	if exists(root, ".ralph/integration.json") {
		t.Fatal("project manifest written")
	}
}

func TestApplyCustomizedSkipped(t *testing.T) {
	root := t.TempDir()
	write1(t, root, ".ralph/integration.json", `{"version":1,"customized":["a.md"],"entries":[]}`)
	write1(t, root, ".ralph/integration.local.json", `{"version":1,"customized":["file:b.md"],"entries":[]}`)
	res := apply(t, root, file("a.md", "x"), file("b.md", "y"), file("c.md", "z"))
	want(t, res, 0, ActionSkipped)
	want(t, res, 1, ActionSkipped)
	want(t, res, 2, ActionInstalled)
	if exists(root, "a.md") || exists(root, "b.md") {
		t.Fatal("customized written")
	}
	if got := manifest(t, root, project.IntegrationFile).Customized; len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("customized = %v", got)
	}
}

func TestApplyFailureDoesNotBlockOthers(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	write1(t, root, ".mcp.json", "nope")
	res := apply(t, root, file("dir.md", "x"), entry(".mcp.json", `{}`), file("ok.md", "y"))
	want(t, res, 0, ActionKept)
	want(t, res, 1, ActionKept)
	want(t, res, 2, ActionInstalled)
	if !strings.Contains(res[0].Detail, "directory") {
		t.Fatalf("detail = %q", res[0].Detail)
	}
}

func TestApplyNeverRemoves(t *testing.T) {
	root := t.TempDir()
	apply(t, root, file("a.md", "x"), file("b.md", "y"))
	apply(t, root, file("a.md", "x"))
	if !exists(root, "b.md") {
		t.Fatal("b.md removed")
	}
	if m := manifest(t, root, project.IntegrationFile); len(m.Entries) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestApplyReadErrorStillSavesEarlierRecords(t *testing.T) {
	root := t.TempDir()
	write1(t, root, "blocker", "x")
	_, err := Apply(context.Background(), root, "fake", []harness.Artifact{file("a.md", "x"), file("blocker/b.md", "y")}, ver)
	if err == nil {
		t.Fatal("no error")
	}
	if len(manifest(t, root, project.IntegrationFile).Entries) != 1 {
		t.Fatal("earlier record lost")
	}
}
