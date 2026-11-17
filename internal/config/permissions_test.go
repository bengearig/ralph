package config

import (
	"strings"
	"testing"
)

func TestHeadlessPermissionsSetting(t *testing.T) {
	r := DefaultRegistry()
	s, ok := r.Lookup("headless.permissions")
	if !ok || s.Scope != ScopeUser || s.Default != "restricted" || s.Description == "" {
		t.Fatalf("headless.permissions = %+v, %v", s, ok)
	}
	f := newFixture(t)
	f.reg = r
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("headless.permissions"); v.Value != "restricted" {
		t.Errorf("default = %q", v.Value)
	}
	for _, good := range []string{"restricted", "auto"} {
		c := mustLoad(t, f.opts(map[string]string{"RALPH_HEADLESS_PERMISSIONS": good}, nil))
		if v, _ := c.Get("headless.permissions"); v.Value != good {
			t.Errorf("%q gave %q", good, v.Value)
		}
	}
	for _, bad := range []string{"harness", ""} {
		g := newFixture(t)
		g.reg = r
		write(t, g.userFile, "headless:\n  permissions: \""+bad+"\"\n")
		ps := loadProblems(t, g, nil)
		if len(ps) != 1 || !strings.Contains(ps[0].Message, "restricted") || !strings.Contains(ps[0].Message, "auto") {
			t.Errorf("%q: problems = %+v", bad, ps)
		}
	}
}

func TestHeadlessPermissionsIgnoredInProjectFile(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.projFile, "headless:\n  permissions: auto\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("headless.permissions"); v.Value != "restricted" {
		t.Errorf("value = %q", v.Value)
	}
	if w := c.Warnings(); len(w) != 1 || w[0].Path != "headless.permissions" || w[0].File != f.projFile {
		t.Errorf("warnings = %+v", w)
	}
}

func TestHeadlessPermissionsFromEnvironmentAndUserFile(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	c := mustLoad(t, f.opts(map[string]string{"RALPH_HEADLESS_PERMISSIONS": "auto"}, nil))
	if v, _ := c.Get("headless.permissions"); v.Value != "auto" || v.Layer != LayerEnv {
		t.Errorf("env value = %+v", v)
	}
	write(t, f.userFile, "headless:\n  permissions: auto\n")
	c = mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("headless.permissions"); v.Value != "auto" {
		t.Errorf("user file value = %+v", v)
	}
}
