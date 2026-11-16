package config

import (
	"strings"
	"testing"
)

func TestGitHubSettings(t *testing.T) {
	r := DefaultRegistry()
	defaults := map[string]struct {
		def   string
		scope Scope
		kind  Kind
	}{
		"github.owner":   {"", ScopeProject, KindString},
		"github.repo":    {"", ScopeProject, KindString},
		"github.api_url": {"https://api.github.com", ScopeUser, KindString},
		"github.token":   {"env:GITHUB_TOKEN", ScopeUser, KindSecret},
	}
	for path, want := range defaults {
		s, ok := r.Lookup(path)
		if !ok || s.Default != want.def || s.Scope != want.scope || s.Kind != want.kind || s.Description == "" {
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
	for _, v := range []string{"", "acme", "my-repo_2.go"} {
		check("github.owner", v, true)
		check("github.repo", v, true)
	}
	for _, v := range []string{"a b", "a/b", "a:b", "é"} {
		check("github.owner", v, false)
		check("github.repo", v, false)
	}
	for _, v := range []string{"https://api.github.com", "https://ghe.example.com/api/v3", "http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:80"} {
		check("github.api_url", v, true)
	}
	for _, v := range []string{"http://api.github.com", "ftp://x", "api.github.com", "", "https://"} {
		check("github.api_url", v, false)
	}
}

func TestGitHubUserOnlySettings(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.projFile, "github:\n  owner: acme\n  api_url: https://evil.example.com\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("github.api_url"); v.Value != "https://api.github.com" || v.Layer != LayerDefault {
		t.Errorf("api_url = %+v", v)
	}
	if v, _ := c.Get("github.owner"); v.Value != "acme" {
		t.Errorf("owner = %+v", v)
	}
	w := c.Warnings()
	if len(w) != 1 || w[0].Path != "github.api_url" {
		t.Fatalf("warnings = %+v", w)
	}
}

func TestGitHubSettingsRejectBadValues(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "github:\n  api_url: http://ghe.example.com\n")
	if _, err := Load(f.opts(nil, nil)); err == nil || !strings.Contains(err.Error(), "github.api_url") {
		t.Fatalf("err = %v", err)
	}

	f = newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "github:\n  token: "+literal+"\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil || strings.Contains(err.Error(), literal) || !strings.Contains(err.Error(), "github.token") {
		t.Fatalf("err = %v", err)
	}
}
