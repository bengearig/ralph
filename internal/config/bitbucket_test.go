package config

import (
	"strings"
	"testing"
)

func TestBitbucketSettings(t *testing.T) {
	r := DefaultRegistry()
	defaults := map[string]struct {
		def   string
		scope Scope
		kind  Kind
	}{
		"bitbucket.workspace": {"", ScopeProject, KindString},
		"bitbucket.repo":      {"", ScopeProject, KindString},
		"bitbucket.api_url":   {"https://api.bitbucket.org/2.0", ScopeUser, KindString},
		"bitbucket.token":     {"env:BITBUCKET_TOKEN", ScopeUser, KindSecret},
		"bitbucket.user":      {"", ScopeUser, KindString},
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
		check("bitbucket.workspace", v, true)
		check("bitbucket.repo", v, true)
	}
	for _, v := range []string{"a b", "a/b", "é"} {
		check("bitbucket.workspace", v, false)
		check("bitbucket.repo", v, false)
	}
	for _, v := range []string{"", "me@example.com"} {
		check("bitbucket.user", v, true)
	}
	check("bitbucket.user", "me @example.com", false)
	check("bitbucket.api_url", "http://127.0.0.1:8080", true)
	check("bitbucket.api_url", "http://api.bitbucket.org", false)
	for _, v := range []string{"auto", "github", "bitbucket"} {
		check("pr.provider", v, true)
	}
	check("pr.provider", "gitlab", false)
}

func TestBitbucketScopes(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "bitbucket:\n  workspace: userws\n  user: me@example.com\n")
	write(t, f.projFile, "bitbucket:\n  workspace: acme\n  repo: widgets\n  api_url: https://evil.example.com\n  token: env:EVIL\n  user: x@example.com\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("bitbucket.workspace"); v.Value != "acme" {
		t.Errorf("workspace = %+v", v)
	}
	if v, _ := c.Get("bitbucket.repo"); v.Value != "widgets" {
		t.Errorf("repo = %+v", v)
	}
	if v, _ := c.Get("bitbucket.api_url"); v.Value != "https://api.bitbucket.org/2.0" {
		t.Errorf("api_url = %+v", v)
	}
	if v, _ := c.Get("bitbucket.user"); v.Value != "me@example.com" {
		t.Errorf("user = %+v", v)
	}
	got := map[string]bool{}
	for _, w := range c.Warnings() {
		got[w.Path] = true
	}
	for _, p := range []string{"bitbucket.api_url", "bitbucket.token", "bitbucket.user"} {
		if !got[p] {
			t.Errorf("no warning for %s: %+v", p, c.Warnings())
		}
	}
}

func TestBitbucketSettingsRejectBadValues(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "bitbucket:\n  api_url: http://bb.example.com\n")
	if _, err := Load(f.opts(nil, nil)); err == nil || !strings.Contains(err.Error(), "bitbucket.api_url") {
		t.Fatalf("err = %v", err)
	}

	f = newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.userFile, "bitbucket:\n  token: "+literal+"\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil || strings.Contains(err.Error(), literal) || !strings.Contains(err.Error(), "bitbucket.token") {
		t.Fatalf("err = %v", err)
	}
}
