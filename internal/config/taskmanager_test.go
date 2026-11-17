package config

import (
	"strings"
	"testing"
)

func problemFor(t *testing.T, path, v string) string {
	t.Helper()
	s, ok := DefaultRegistry().Lookup(path)
	if !ok {
		t.Fatalf("%s is not registered", path)
	}
	return valueProblem(s, v)
}

func expect(t *testing.T, path string, accepted []string, rejected []string) {
	t.Helper()
	for _, v := range accepted {
		if p := problemFor(t, path, v); p != "" {
			t.Errorf("%s=%q rejected: %s", path, v, p)
		}
	}
	for _, v := range rejected {
		if p := problemFor(t, path, v); p == "" {
			t.Errorf("%s=%q accepted", path, v)
		}
	}
}

func TestTaskManagerSettingsDefaults(t *testing.T) {
	r := DefaultRegistry()
	for path, want := range map[string]string{
		"taskmanager.provider": "none",
		"jira.site":            "",
		"jira.user":            "",
		"jira.token":           "env:JIRA_TOKEN",
		"jira.project":         "",
		"jira.type.brief":      "Feature",
		"jira.type.slice":      "Task",
		"jira.type.task":       "",
		"jira.sprint":          "",
		"jira.board":           "",
		"jira.auto_sync":       "true",
		"jira.poll_interval":   "5m",
		"jira.sync_timeout":    "15s",
		"jira.timeout":         "30s",
	} {
		s, ok := r.Lookup(path)
		if !ok || s.Default != want || s.Description == "" {
			t.Errorf("%s = %+v, %v; want default %q", path, s, ok, want)
		}
	}
	for _, path := range []string{"jira.states", "jira.flow", "jira.sprint_levels"} {
		if s, ok := r.Lookup(path); !ok || s.Kind != KindList || s.Default != "" {
			t.Errorf("%s = %+v, %v", path, s, ok)
		}
	}
}

func TestTaskManagerSettingsScopes(t *testing.T) {
	r := DefaultRegistry()
	user := map[string]bool{"jira.site": true, "jira.user": true, "jira.token": true}
	for _, path := range []string{
		"taskmanager.provider", "jira.site", "jira.user", "jira.token", "jira.project", "jira.type.brief",
		"jira.type.slice", "jira.type.task", "jira.states", "jira.flow", "jira.sprint", "jira.board",
		"jira.sprint_levels", "jira.auto_sync", "jira.poll_interval", "jira.sync_timeout", "jira.timeout",
	} {
		s, ok := r.Lookup(path)
		if !ok {
			t.Errorf("%s is not registered", path)
			continue
		}
		if want := user[path]; (s.Scope == ScopeUser) != want {
			t.Errorf("%s user-only = %v, want %v", path, s.Scope == ScopeUser, want)
		}
	}
	for _, path := range []string{"jira.site", "jira.user", "jira.token"} {
		s, _ := r.Lookup(path)
		if !strings.Contains(s.Description, "jira.site") && path != "jira.site" {
			t.Errorf("%s description does not name jira.site", path)
		}
		if !strings.Contains(s.Description, "sent to") {
			t.Errorf("%s description does not say ticket text is sent", path)
		}
	}
	if s, _ := r.Lookup("jira.token"); s.Kind != KindSecret {
		t.Errorf("jira.token kind = %v", s.Kind)
	}
	expect(t, "taskmanager.provider", []string{"none", "jira"}, []string{"", "github"})
	expect(t, "jira.auto_sync", []string{"true", "false"}, []string{"", "yes"})
}

func TestJiraSiteValidation(t *testing.T) {
	expect(t, "jira.site",
		[]string{"", "https://x.atlassian.net", "https://x.atlassian.net/", "http://localhost:8080", "http://127.0.0.1:9", "http://[::1]:80"},
		[]string{"http://x.atlassian.net", "ftp://x", "x.atlassian.net", "https://u:p@x", "https://x?q=1", "https://x?", "https://x/#f", "https://x#", "https://", "https:///path", "https://:443", "http://"})
	if p := problemFor(t, "jira.site", "https://u:p@x"); !strings.Contains(p, "jira.site must not contain credentials") {
		t.Errorf("problem = %q", p)
	}
	if p := problemFor(t, "jira.site", "https://u:p@x"); strings.Contains(p, ":p@") {
		t.Errorf("problem leaks the credentials: %q", p)
	}
}

func TestJiraUserValidation(t *testing.T) {
	expect(t, "jira.user",
		[]string{"", "a@b", "me@example.com", strings.Repeat("a", 252) + "@b"},
		[]string{"me", "me @example.com", "me@exa\tmple.com", "me@\x00x", strings.Repeat("a", 253) + "@b"})
}

func TestJiraProjectValidation(t *testing.T) {
	expect(t, "jira.project",
		[]string{"", "ABC", "AB_1", "AB", "A" + strings.Repeat("B", 19)},
		[]string{"A", "abc", "1AB", "_AB", "AB-1", "AB C", "A" + strings.Repeat("B", 20), "ÀB"})
}

func TestJiraTypeValidation(t *testing.T) {
	for _, path := range []string{"jira.type.brief", "jira.type.slice", "jira.type.task"} {
		expect(t, path,
			[]string{"", "Feature", "Sub-task", "A B", strings.Repeat("a", 60)},
			[]string{" Task", "Task ", "Ta\nsk", "Ta\x00sk", strings.Repeat("a", 61)})
	}
}

func TestJiraStatesItems(t *testing.T) {
	expect(t, "jira.states",
		[]string{`[]`, `["TODO=Backlog"]`, `["TODO=A","TODO=B"]`, `["MERGED=Done","EMPTY=Idea","IN_REVIEW=In review"]`, `["DONE=a=b"]`},
		[]string{`["TODO"]`, `["todo=Backlog"]`, `["UNKNOWN=x"]`, `["TODO="]`, `["TODO=` + strings.Repeat("a", 61) + `"]`, `["TODO=a\nb"]`, `["=x"]`})
}

func TestJiraFlowItems(t *testing.T) {
	expect(t, "jira.flow",
		[]string{`[]`, `["To Do","In Progress","Done"]`, `["A","A"]`, `["` + strings.Repeat("a", 60) + `"]`},
		[]string{`[""]`, `["` + strings.Repeat("a", 61) + `"]`, `["a\u0000b"]`})
}

func TestJiraBoardValidation(t *testing.T) {
	expect(t, "jira.board", []string{"7", "1", "", "1000000"}, []string{"0", "x", "-1", "1.5", "07", " 7"})
}

func TestJiraSprintValidation(t *testing.T) {
	expect(t, "jira.sprint", []string{"", "Sprint 1", strings.Repeat("a", 100)}, []string{strings.Repeat("a", 101), "a\nb"})
}

func TestJiraSprintLevelsItems(t *testing.T) {
	expect(t, "jira.sprint_levels",
		[]string{`[]`, `["brief"]`, `["slice","task"]`, `["brief","slice","task"]`},
		[]string{`["epic"]`, `["Brief"]`, `[""]`})
}

func TestJiraDurations(t *testing.T) {
	expect(t, "jira.poll_interval", []string{"1m", "5m", "24h", "90s"}, []string{"0", "59s", "25h", "24h1s", "x", "-5m", ""})
	for _, path := range []string{"jira.sync_timeout", "jira.timeout"} {
		expect(t, path, []string{"1s", "30s", "5m"}, []string{"0", "999ms", "5m1s", "x", "-1s"})
	}
}

func TestJiraSettingsIgnoredInProjectFile(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.projFile, "jira:\n  site: https://evil.example.com\n  user: a@b\n  token: env:EVIL\n  project: ABC\n")
	c := mustLoad(t, f.opts(nil, nil))
	got := map[string]bool{}
	for _, w := range c.Warnings() {
		got[w.Path] = true
	}
	if len(got) != 3 || !got["jira.site"] || !got["jira.user"] || !got["jira.token"] {
		t.Fatalf("warnings = %+v", c.Warnings())
	}
	if v, ok := c.Get("jira.site"); ok {
		t.Errorf("jira.site = %+v", v)
	}
	if v, _ := c.Get("jira.token"); v.Value != "env:JIRA_TOKEN" {
		t.Errorf("jira.token = %+v", v)
	}
	if v, _ := c.Get("jira.project"); v.Value != "ABC" {
		t.Errorf("jira.project = %+v", v)
	}
}

func TestJiraSettingsFromEnvironment(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	c := mustLoad(t, f.opts(map[string]string{
		"RALPH_JIRA_SITE":          "https://x.atlassian.net",
		"RALPH_JIRA_POLL_INTERVAL": "10m",
		"RALPH_JIRA_FLOW":          `["To Do","Done"]`,
	}, nil))
	if v, _ := c.Get("jira.site"); v.Value != "https://x.atlassian.net" || v.Layer != LayerEnv {
		t.Errorf("jira.site = %+v", v)
	}
	if v, _ := c.Get("jira.poll_interval"); v.Value != "10m" {
		t.Errorf("poll = %+v", v)
	}
	if got, ok := c.List("jira.flow"); !ok || len(got) != 2 {
		t.Errorf("flow = %v, %v", got, ok)
	}
	_, err := Load(f.opts(map[string]string{"RALPH_JIRA_SITE": "http://x.atlassian.net"}, nil))
	if err == nil || !strings.Contains(err.Error(), "jira.site") {
		t.Errorf("err = %v", err)
	}
}
