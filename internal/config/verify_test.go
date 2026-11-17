package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestVerifySettings(t *testing.T) {
	r := DefaultRegistry()
	c, ok := r.Lookup("verify.commands")
	if !ok || c.Kind != KindList || c.Scope != ScopeProject || c.Default != "" || c.Description == "" {
		t.Fatalf("verify.commands = %+v, %v", c, ok)
	}
	if err := c.ValidateItem("go test ./..."); err != nil {
		t.Errorf("command rejected: %v", err)
	}
	for _, bad := range []string{"", "  \t"} {
		if err := c.ValidateItem(bad); err == nil {
			t.Errorf("blank item %q accepted", bad)
		}
	}
	d, ok := r.Lookup("verify.timeout")
	if !ok || d.Default != "10m" || d.Scope != ScopeProject || d.Description == "" {
		t.Fatalf("verify.timeout = %+v, %v", d, ok)
	}
	for _, v := range []string{"1s", "2h", "10m"} {
		if err := d.Validate(v); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"500ms", "3h", "abc", ""} {
		if err := d.Validate(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestSourceText(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "proj")
	inside := filepath.Join(root, ".ralph", "project.yaml")
	outside := filepath.Join(string(filepath.Separator), "home", "u", "user.yaml")
	for name, tc := range map[string]struct {
		v    Value
		want string
	}{
		"inside":  {Value{Source: Source{File: inside, Line: 4}}, filepath.Join(".ralph", "project.yaml") + ":4"},
		"outside": {Value{Source: Source{File: outside, Line: 2}}, outside + ":2"},
		"env":     {Value{Source: Source{Name: "RALPH_LOG_LEVEL"}}, "RALPH_LOG_LEVEL"},
		"flag":    {Value{Source: Source{Name: "--log-level"}}, "--log-level"},
		"none":    {Value{}, "-"},
	} {
		if got := tc.v.SourceText(root); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestHeadlessAllowSetting(t *testing.T) {
	r := DefaultRegistry()
	s, ok := r.Lookup("headless.allow")
	if !ok || s.Kind != KindList || s.Scope != ScopeUser || s.Default != "" || s.Description == "" {
		t.Fatalf("headless.allow = %+v, %v", s, ok)
	}
	for _, good := range []string{"npm test *", `go test \*`, strings.Repeat("x", 500)} {
		if err := s.ValidateItem(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
	for name, bad := range map[string]string{"empty": "", "blank": " \t ", "newline": "a\nb", "control": "a\x01b", "long": strings.Repeat("x", 501)} {
		if err := s.ValidateItem(bad); err == nil {
			t.Errorf("%s item accepted", name)
		}
	}

	f := newFixture(t)
	f.reg = r
	if c := mustLoad(t, f.opts(nil, nil)); len(c.Warnings()) != 0 {
		t.Errorf("warnings = %+v", c.Warnings())
	} else if l, ok := c.List("headless.allow"); ok || len(l) != 0 {
		t.Errorf("default = %q, %v", l, ok)
	}
	write(t, f.userFile, "headless:\n  allow:\n    - make lint\n    - npm test *\n")
	c := mustLoad(t, f.opts(nil, nil))
	if l, _ := c.List("headless.allow"); !slices.Equal(l, []string{"make lint", "npm test *"}) {
		t.Errorf("user file list = %q", l)
	}
	c = mustLoad(t, f.opts(map[string]string{"RALPH_HEADLESS_ALLOW": `["a", "b c"]`}, nil))
	if l, _ := c.List("headless.allow"); !slices.Equal(l, []string{"a", "b c"}) {
		t.Errorf("env list = %q", l)
	}
	if v, _ := c.Get("headless.allow"); v.Layer != LayerEnv {
		t.Errorf("layer = %v", v.Layer)
	}
	if ps := loadProblems(t, f, map[string]string{"RALPH_HEADLESS_ALLOW": `["a", " "]`}); len(ps) != 1 {
		t.Errorf("problems = %+v", ps)
	}

	g := newFixture(t)
	g.reg = r
	write(t, g.localFi, "headless:\n  allow:\n    - local cmd\n")
	if l, _ := mustLoad(t, g.opts(nil, nil)).List("headless.allow"); !slices.Equal(l, []string{"local cmd"}) {
		t.Errorf("local file list = %q", l)
	}
	h := newFixture(t)
	h.reg = r
	write(t, h.projFile, "headless:\n  allow:\n    - evil\n")
	c = mustLoad(t, h.opts(nil, nil))
	if l, _ := c.List("headless.allow"); len(l) != 0 {
		t.Errorf("project file list applied: %q", l)
	}
	if w := c.Warnings(); len(w) != 1 || w[0].Path != "headless.allow" || w[0].File != h.projFile {
		t.Errorf("warnings = %+v", w)
	}
}

func TestLoopPromptSetting(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("loop.prompt_documents_bytes")
	if !ok || s.Default != "0" || s.Scope != ScopeProject || s.Description == "" {
		t.Fatalf("loop.prompt_documents_bytes = %+v, %v", s, ok)
	}
	for _, v := range []string{"0", "60000"} {
		if err := s.Validate(v); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"-1", "abc", "1.5", ""} {
		if err := s.Validate(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}
