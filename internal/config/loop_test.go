package config

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness/all"
)

func TestLoopSettings(t *testing.T) {
	r := DefaultRegistry()
	for _, path := range []string{"loop.harness", "loop.model"} {
		s, ok := r.Lookup(path)
		if !ok || s.Scope != ScopeProject || s.Default != "" || s.Description == "" {
			t.Fatalf("%s = %+v, %v", path, s, ok)
		}
	}
	cases := []struct {
		path, value, problem string
	}{
		{"loop.harness", "claude-code", ""},
		{"loop.harness", "opencode", ""},
		{"loop.harness", "", ""},
		{"loop.harness", "emacs", "claude-code, opencode"},
		{"loop.model", "haiku", ""},
		{"loop.model", "", ""},
		{"loop.model", "-x", `"-"`},
		{"loop.model", "a b", "space"},
	}
	for _, tc := range cases {
		f := newFixture(t)
		f.reg = r
		write(t, f.projFile, "loop:\n  "+strings.TrimPrefix(tc.path, "loop.")+": \""+tc.value+"\"\n")
		if tc.problem == "" {
			c := mustLoad(t, f.opts(nil, nil))
			if v, _ := c.Get(tc.path); v.Value != tc.value {
				t.Errorf("%s=%q: got %q", tc.path, tc.value, v.Value)
			}
			continue
		}
		ps := loadProblems(t, f, nil)
		if len(ps) != 1 || !strings.Contains(ps[0].Message, tc.problem) {
			t.Errorf("%s=%q: problems = %+v, want %q", tc.path, tc.value, ps, tc.problem)
		}
	}
}

func TestLoopHarnessNamesMatchAdapters(t *testing.T) {
	if got, want := strings.Join(harnessNames, ","), strings.Join(all.Names(), ","); got != want {
		t.Fatalf("harnessNames = %s, adapters = %s", got, want)
	}
}
