package config

import (
	"strings"
	"testing"
)

func TestPRSettings(t *testing.T) {
	r := DefaultRegistry()
	defaults := map[string]string{
		"pr.provider": "auto", "pr.remote": "origin", "pr.base_branch": "",
		"pr.draft": "true", "pr.timeout": "30s",
	}
	for path, want := range defaults {
		s, ok := r.Lookup(path)
		if !ok || s.Default != want || s.Description == "" || s.Scope != ScopeProject {
			t.Errorf("%s = %+v, %v", path, s, ok)
		}
	}
	check := func(path, v string, ok bool) {
		t.Helper()
		s, _ := r.Lookup(path)
		if p := valueProblem(s, v); (p == "") != ok {
			t.Errorf("%s=%q: problem %q, accepted want %v", path, v, p, ok)
		}
	}
	check("pr.provider", "auto", true)
	check("pr.provider", "github", true)
	check("pr.provider", "gitlab", false)
	check("pr.draft", "true", true)
	check("pr.draft", "false", true)
	check("pr.draft", "yes", false)
	for _, v := range []string{"0s", "500ms", "6m", "abc"} {
		check("pr.timeout", v, false)
	}
	for _, v := range []string{"1s", "30s", "5m"} {
		check("pr.timeout", v, true)
	}
	for _, v := range []string{"", "main", "release/1.0", "feat/x-y"} {
		check("pr.base_branch", v, true)
	}
	for _, v := range []string{"a b", "a..b", "-x", "a/", "a.lock", "a@{b", "a:b", "/a"} {
		check("pr.base_branch", v, false)
	}
	check("pr.remote", "origin", true)
	check("pr.remote", " ", false)
	check("pr.remote", "my remote", false)
}

func TestPRProviderAuto(t *testing.T) {
	s, _ := DefaultRegistry().Lookup("pr.provider")
	if p := valueProblem(s, "auto"); p != "" {
		t.Errorf("auto rejected: %s", p)
	}
	if p := valueProblem(s, "gitlab"); !strings.Contains(p, "auto") || !strings.Contains(p, "github") {
		t.Errorf("problem = %q", p)
	}
}
