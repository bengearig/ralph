package harnesstest

import (
	"context"
	"fmt"
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
