package harnesstest

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/integration"
)

const (
	sectionBegin = "<!-- ralph:begin -->"
	userText     = "# Project notes\n\nUser text that must survive.\n"
	jsoncText    = "{\n  // keep this comment\n  \"theme\": \"dark\",\n}\n"
)

type installState struct {
	name  string
	files map[string]string
}

func installStates() []installState {
	return []installState{
		{"empty", nil},
		{"CLAUDE.md", map[string]string{"CLAUDE.md": userText}},
		{"AGENTS.md", map[string]string{"AGENTS.md": userText}},
		{"CLAUDE.md and AGENTS.md", map[string]string{"CLAUDE.md": userText, "AGENTS.md": userText + "more\n"}},
		{"opencode.jsonc", map[string]string{"opencode.jsonc": jsoncText}},
		{"opencode.json", map[string]string{"opencode.json": "{\"theme\":\"dark\"}\n"}},
		{".mcp.json", map[string]string{".mcp.json": "{\"mcpServers\":{\"other\":{\"command\":\"x\"}}}\n"}},
	}
}

// CheckInstall applies adapters in order to projects in several states and
// fails if the installer damages user files, writes outside the artifacts, is
// not idempotent, or leaves anything out of date.
func CheckInstall(t *testing.T, adapters []harness.Adapter) {
	t.Helper()
	checkInstall(t, adapters)
}

func checkInstall(r reporter, adapters []harness.Adapter) {
	r.Helper()
	for _, st := range installStates() {
		forward, ok := installOne(r, adapters, st)
		if !ok || len(adapters) < 2 {
			continue
		}
		reversed := slices.Clone(adapters)
		slices.Reverse(reversed)
		back, ok := installOne(r, reversed, st)
		if !ok {
			continue
		}
		compareOrders(r, st, forward, back)
	}
}

func compareOrders(r reporter, st installState, a, b map[string][]byte) {
	r.Helper()
	skip := func(p string) bool {
		_, hasClaude := st.files["CLAUDE.md"]
		_, hasAgents := st.files["AGENTS.md"]
		return !hasClaude && !hasAgents && (p == "CLAUDE.md" || p == "AGENTS.md")
	}
	names := func(m map[string][]byte) []string {
		var out []string
		for p := range m {
			if !skip(p) {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	if x, y := names(a), names(b); !slices.Equal(x, y) {
		r.Errorf("state %s: the order of adapters changes the files: %v and %v", st.name, x, y)
	}
}

func installOne(r reporter, adapters []harness.Adapter, st installState) (map[string][]byte, bool) {
	r.Helper()
	dir, err := tempDir()
	if err != nil {
		r.Errorf("state %s: creating temporary directory: %v", st.name, err)
		return nil, false
	}
	defer removeAll(dir)
	for p, c := range st.files {
		if err := writeFile(dir, p, c); err != nil {
			r.Errorf("state %s: %v", st.name, err)
			return nil, false
		}
	}
	before, err := snapshot(dir)
	if err != nil {
		r.Errorf("state %s: %v", st.name, err)
		return nil, false
	}

	order := make([]string, len(adapters))
	for i, a := range adapters {
		order[i] = a.Name()
	}
	label := fmt.Sprintf("state %s, adapters %s", st.name, strings.Join(order, ", "))

	allowed := map[string]bool{}
	var sections []string
	pass := func(n int) (map[string][]byte, bool) {
		for _, a := range adapters {
			in, err := integration.InputsFor(dir)
			if err != nil {
				r.Errorf("%s: pass %d: InputsFor: %v", label, n, err)
				return nil, false
			}
			arts, err := artifactsOf(a, in)
			if err != nil {
				r.Errorf("%s: pass %d: %s: Artifacts: %v", label, n, a.Name(), err)
				return nil, false
			}
			for _, art := range arts {
				allowed[art.Path] = true
				allowed[art.Path+".new"] = true
				if art.Kind == harness.KindSection && !slices.Contains(sections, art.Path) {
					sections = append(sections, art.Path)
				}
			}
			results, err := integration.Apply(context.Background(), dir, a.Name(), arts, "conformance")
			if err != nil {
				r.Errorf("%s: pass %d: %s: Apply: %v", label, n, a.Name(), err)
				return nil, false
			}
			checkResults(r, label, n, st, results)
		}
		snap, err := snapshot(dir)
		if err != nil {
			r.Errorf("%s: %v", label, err)
			return nil, false
		}
		return snap, true
	}

	first, ok := pass(1)
	if !ok {
		return nil, false
	}
	checkUserFiles(r, label, st, first)
	checkStray(r, label, before, first, allowed)
	for _, p := range sections {
		if n := bytes.Count(first[p], []byte(sectionBegin)); n != 1 {
			r.Errorf("%s: %s holds ralph's section %d times, want once", label, p, n)
		}
	}

	second, ok := pass(2)
	if !ok {
		return nil, false
	}
	if d := diff(first, second); d != "" {
		r.Errorf("%s: a second pass changed %s", label, d)
	}

	outdated, err := integration.Check(context.Background(), dir, adapters)
	if err != nil {
		r.Errorf("%s: Check: %v", label, err)
	}
	for _, o := range outdated {
		r.Errorf("%s: %s reported out of date after install", label, o.Path)
	}
	return first, true
}

func checkResults(r reporter, label string, pass int, st installState, results []integration.Result) {
	r.Helper()
	for _, res := range results {
		_, existed := st.files[res.Path]
		jsonc := existed && strings.HasSuffix(res.Path, ".jsonc")
		switch res.Action {
		case integration.ActionKept:
			if !jsonc || !strings.Contains(res.Detail, "ralph does not edit it") {
				r.Errorf("%s: pass %d: %s was kept: %s", label, pass, res.Path, res.Detail)
			}
		case integration.ActionInstalled, integration.ActionAdopted, integration.ActionUnchanged:
			if jsonc && res.Action == integration.ActionInstalled {
				r.Errorf("%s: pass %d: %s in a jsonc file was %s", label, pass, res.Path, res.Action)
			}
			if pass == 2 && res.Action != integration.ActionUnchanged {
				r.Errorf("%s: pass %d: %s was %s, want unchanged", label, pass, res.Path, res.Action)
			}
		default:
			r.Errorf("%s: pass %d: %s was %s", label, pass, res.Path, res.Action)
		}
	}
}

func checkUserFiles(r reporter, label string, st installState, after map[string][]byte) {
	r.Helper()
	for p, c := range st.files {
		got, ok := after[p]
		switch {
		case !ok:
			r.Errorf("%s: user file %s was removed", label, p)
		case strings.HasSuffix(p, ".jsonc"):
			if string(got) != c {
				r.Errorf("%s: %s was changed; it holds user text and must stay byte for byte", label, p)
			}
		case strings.HasSuffix(p, ".md"):
			if !bytes.HasPrefix(got, []byte(c)) {
				r.Errorf("%s: user text in %s was changed", label, p)
			}
		default:
			for _, key := range []string{`"theme"`, `"other"`} {
				if strings.Contains(c, key) && !bytes.Contains(got, []byte(key)) {
					r.Errorf("%s: user key %s in %s was lost", label, key, p)
				}
			}
		}
	}
}

func checkStray(r reporter, label string, before, after map[string][]byte, allowed map[string]bool) {
	r.Helper()
	for p, c := range after {
		if old, ok := before[p]; ok && bytes.Equal(old, c) {
			continue
		}
		if allowed[p] || p == ".ralph/integration.json" || p == ".ralph/integration.local.json" {
			continue
		}
		r.Errorf("%s: unexpected file %s was created or changed", label, p)
	}
}

func diff(a, b map[string][]byte) string {
	var changed []string
	for p, c := range a {
		if d, ok := b[p]; !ok || !bytes.Equal(c, d) {
			changed = append(changed, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return strings.Join(changed, ", ")
}

func writeFile(dir, rel, content string) error {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

func snapshot(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	return out, err
}
