package config

import (
	"slices"
	"strings"
	"testing"
)

func TestFollowUpAutoSetting(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("follow_up.auto")
	if !ok || s.Kind != KindList || s.Scope != ScopeUser || s.Default != "" {
		t.Fatalf("follow_up.auto = %+v, %v", s, ok)
	}
	for _, w := range []string{"ralph apply", "ralph monitor --watch --apply", "same checks"} {
		if !strings.Contains(s.Description, w) {
			t.Errorf("description lacks %q: %s", w, s.Description)
		}
	}
	f := newFixture(t)
	f.reg = DefaultRegistry()
	if c := mustLoad(t, f.opts(nil, nil)); len(c.Warnings()) != 0 {
		t.Errorf("warnings = %+v", c.Warnings())
	} else if l, ok := c.List("follow_up.auto"); ok || len(l) != 0 {
		t.Errorf("default = %q, %v", l, ok)
	}
	for name, tc := range map[string]struct {
		value string
		want  []string
	}{
		"close":    {`["close"]`, []string{"close"}},
		"feedback": {`["feedback"]`, []string{"feedback"}},
		"both":     {`["close", "feedback"]`, []string{"close", "feedback"}},
		"continue": {`["close", "feedback", "continue"]`, []string{"close", "feedback", "continue"}},
	} {
		c := mustLoad(t, f.opts(map[string]string{"RALPH_FOLLOW_UP_AUTO": tc.value}, nil))
		if l, _ := c.List("follow_up.auto"); !slices.Equal(l, tc.want) {
			t.Errorf("%s: list = %q, want %q", name, l, tc.want)
		}
	}
	for name, value := range map[string]string{"all": `["all"]`, "empty": `["close", ""]`, "mixed": `["close", "all"]`} {
		ps := loadProblems(t, f, map[string]string{"RALPH_FOLLOW_UP_AUTO": value})
		if len(ps) != 1 || !strings.Contains(ps[0].Message, "close, feedback, continue") {
			t.Errorf("%s: problems = %+v", name, ps)
		}
	}
}

func TestFollowUpAutoIgnoredInProjectFile(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.projFile, "follow_up:\n  auto:\n    - close\n")
	c := mustLoad(t, f.opts(nil, nil))
	if l, _ := c.List("follow_up.auto"); len(l) != 0 {
		t.Errorf("project file list applied: %q", l)
	}
	if w := c.Warnings(); len(w) != 1 || w[0].Path != "follow_up.auto" || w[0].File != f.projFile {
		t.Errorf("warnings = %+v", w)
	}
	g := newFixture(t)
	g.reg = DefaultRegistry()
	write(t, g.userFile, "follow_up:\n  auto:\n    - feedback\n")
	write(t, g.localFi, "follow_up:\n  auto:\n    - close\n")
	if l, _ := mustLoad(t, g.opts(nil, nil)).List("follow_up.auto"); !slices.Equal(l, []string{"close"}) {
		t.Errorf("local file list = %q", l)
	}
}

func TestFollowUpAutoFromEnvironment(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "follow_up:\n  auto:\n    - feedback\n")
	c := mustLoad(t, f.opts(map[string]string{"RALPH_FOLLOW_UP_AUTO": `["close"]`}, nil))
	if l, _ := c.List("follow_up.auto"); !slices.Equal(l, []string{"close"}) {
		t.Errorf("env list = %q", l)
	}
	if v, _ := c.Get("follow_up.auto"); v.Layer != LayerEnv {
		t.Errorf("layer = %v", v.Layer)
	}
}

func TestFollowUpAutoAcceptsContinue(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	c := mustLoad(t, f.opts(map[string]string{"RALPH_FOLLOW_UP_AUTO": `["close", "feedback", "continue"]`}, nil))
	if l, _ := c.List("follow_up.auto"); !slices.Equal(l, []string{"close", "feedback", "continue"}) {
		t.Errorf("list = %q", l)
	}
	ps := loadProblems(t, f, map[string]string{"RALPH_FOLLOW_UP_AUTO": `["start"]`})
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "close, feedback, continue") {
		t.Errorf("problems = %+v", ps)
	}
	write(t, f.projFile, "follow_up:\n  auto:\n    - continue\n")
	if l, _ := mustLoad(t, f.opts(nil, nil)).List("follow_up.auto"); len(l) != 0 {
		t.Errorf("project file set follow_up.auto: %q", l)
	}
}
