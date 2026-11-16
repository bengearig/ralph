package harnesstest

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

type recorder struct{ msgs []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func (r *recorder) mentions(s string) bool {
	for _, m := range r.msgs {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

type fake struct {
	name string
	arts func(harness.Inputs) []harness.Artifact

	mu sync.Mutex
	n  int
}

func (f *fake) Name() string                                     { return f.name }
func (f *fake) Detect(context.Context, string) harness.Detection { return harness.Detection{} }
func (f *fake) Artifacts(in harness.Inputs) ([]harness.Artifact, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return f.arts(in), nil
}

func good(in harness.Inputs) []harness.Artifact {
	return []harness.Artifact{
		{Kind: harness.KindFile, Scope: harness.ScopeProject, Path: "cmds/" + in.Instructions[0].Name + ".md", Content: []byte(in.Instructions[0].Body)},
		{Kind: harness.KindSection, Scope: harness.ScopeProject, Path: "NOTES.md", Content: []byte(in.ProjectNotes)},
		{Kind: harness.KindJSONEntry, Scope: harness.ScopeProject, Path: "mcp.json", KeyPath: []string{"servers", in.MCP.Name}, Content: []byte(`{"command":"` + in.MCP.Command + `"}`)},
	}
}

func TestCheckAcceptsGoodAdapter(t *testing.T) {
	a := &fake{name: "fake", arts: good}
	Check(t, a)
	CheckDeterministic(t, a)
	r := &recorder{}
	check(r, a)
	if len(r.msgs) != 0 {
		t.Errorf("unexpected failures: %v", r.msgs)
	}
}

func TestCheckRejectsDuplicateIDs(t *testing.T) {
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		g := good(in)
		return append(g, g[0])
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("duplicate") {
		t.Errorf("duplicate IDs not rejected: %v", r.msgs)
	}
}

func TestCheckRejectsAbsolutePath(t *testing.T) {
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		g := good(in)
		g[0].Path = "/etc/cmd.md"
		return g
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("escapes") || !r.mentions("invalid artifact") {
		t.Errorf("absolute path not rejected: %v", r.msgs)
	}
}

func TestCheckRejectsNondeterminism(t *testing.T) {
	var a *fake
	a = &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		g := good(in)
		g[0].Content = []byte(fmt.Sprintf("call %d", a.n))
		return g
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("not deterministic") {
		t.Errorf("nondeterminism not rejected: %v", r.msgs)
	}
}

func TestCheckRejectsMarkersInSection(t *testing.T) {
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		g := good(in)
		g[1].Content = []byte("x <!-- ralph:begin --> y")
		return g
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("marker") {
		t.Errorf("marker in section not rejected: %v", r.msgs)
	}
}

func TestCheckRejectsBadName(t *testing.T) {
	for _, name := range []string{"", "Fake", "my fake"} {
		r := &recorder{}
		check(r, &fake{name: name, arts: good})
		if !r.mentions("adapter name") {
			t.Errorf("name %q not rejected: %v", name, r.msgs)
		}
	}
}

func viewKey(fsys fs.FS) string {
	if fsys == nil {
		return "nil"
	}
	var names []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	return "[" + strings.Join(names, ",") + "]"
}

func TestCheckRunsWithEachProjectView(t *testing.T) {
	seen := map[string]bool{}
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		seen[viewKey(in.Project)] = true
		return good(in)
	}}
	r := &recorder{}
	check(r, a)
	if len(r.msgs) != 0 {
		t.Errorf("unexpected failures: %v", r.msgs)
	}
	for _, want := range []string{"nil", "[]", "[CLAUDE.md]", "[AGENTS.md]", "[AGENTS.md,CLAUDE.md]", "[opencode.json]", "[opencode.jsonc]", "[.opencode/opencode.json]"} {
		if !seen[want] {
			t.Errorf("view %s never used; saw %v", want, seen)
		}
	}
}

func TestCheckRejectsNondeterminismDependingOnProject(t *testing.T) {
	n := 0
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		g := good(in)
		if viewKey(in.Project) == "[AGENTS.md]" {
			n++
			g[0].Content = []byte(fmt.Sprintf("call %d", n))
		}
		return g
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("not deterministic") {
		t.Errorf("project-dependent nondeterminism not rejected: %v", r.msgs)
	}
}

func TestCheckRejectsPanic(t *testing.T) {
	a := &fake{name: "fake", arts: func(in harness.Inputs) []harness.Artifact {
		if in.Project == nil {
			panic("nil project")
		}
		return good(in)
	}}
	r := &recorder{}
	check(r, a)
	if !r.mentions("panicked") {
		t.Errorf("panic not reported: %v", r.msgs)
	}
}

func sharedAdapters(content1, content2 string, kind2 harness.Kind) []harness.Adapter {
	mk := func(name, content string, kind harness.Kind, path string) *fake {
		return &fake{name: name, arts: func(harness.Inputs) []harness.Artifact {
			return []harness.Artifact{{Kind: kind, Scope: harness.ScopeProject, Path: path, Content: []byte(content)}}
		}}
	}
	return []harness.Adapter{
		mk("one", content1, harness.KindSection, "AGENTS.md"),
		mk("two", content2, kind2, "AGENTS.md"),
	}
}

func TestCheckAllAcceptsIdenticalSharedArtifact(t *testing.T) {
	r := &recorder{}
	checkAll(r, sharedAdapters("same", "same", harness.KindSection))
	if len(r.msgs) != 0 {
		t.Errorf("unexpected failures: %v", r.msgs)
	}
}

func TestCheckAllRejectsSharedIDWithDifferentContent(t *testing.T) {
	r := &recorder{}
	checkAll(r, sharedAdapters("a", "b", harness.KindSection))
	if !r.mentions("different content") {
		t.Errorf("not rejected: %v", r.msgs)
	}
}

func TestCheckAllRejectsPathClaimedWithDifferentKinds(t *testing.T) {
	r := &recorder{}
	checkAll(r, sharedAdapters("a", "a", harness.KindFile))
	if !r.mentions("claims") {
		t.Errorf("not rejected: %v", r.msgs)
	}
}

func TestCheckAllAllowsJSONKindsToSharePath(t *testing.T) {
	mk := func(name string, kind harness.Kind, key []string, content string) *fake {
		return &fake{name: name, arts: func(harness.Inputs) []harness.Artifact {
			return []harness.Artifact{{Kind: kind, Scope: harness.ScopeProject, Path: "opencode.json", KeyPath: key, Content: []byte(content)}}
		}}
	}
	r := &recorder{}
	checkAll(r, []harness.Adapter{
		mk("one", harness.KindJSONEntry, []string{"mcp", "ralph"}, `{"a":1}`),
		mk("two", harness.KindJSONArrayItem, []string{"permission", "allow"}, `"x"`),
	})
	if len(r.msgs) != 0 {
		t.Errorf("unexpected failures: %v", r.msgs)
	}
}
