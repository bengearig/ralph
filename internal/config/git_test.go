package config

import (
	"strings"
	"testing"
)

func TestGitSettings(t *testing.T) {
	r := DefaultRegistry()
	defaults := map[string]string{
		"git.branch_template": "ralph/{{lower .ID}}-{{.Slug}}",
		"git.commit_template": "{{.ID}}: {{.Title}}",
		"git.commit_trailer":  "true",
	}
	for path, want := range defaults {
		s, ok := r.Lookup(path)
		if !ok || s.Default != want || s.Description == "" || s.Scope != ScopeProject {
			t.Errorf("%s = %+v, %v", path, s, ok)
		}
		if p := valueProblem(s, s.Default); p != "" {
			t.Errorf("%s default rejected: %s", path, p)
		}
	}
	check := func(path, v string, ok bool) {
		t.Helper()
		s, _ := r.Lookup(path)
		p := valueProblem(s, v)
		if (p == "") != ok {
			t.Errorf("%s=%q: problem %q, accepted want %v", path, v, p, ok)
		}
		if p != "" && !strings.Contains(p, path) {
			t.Errorf("%s=%q: problem %q does not name the setting", path, v, p)
		}
	}
	check("git.branch_template", "feat/{{.Slug}}", true)
	check("git.branch_template", "{{.ID", false)
	check("git.branch_template", "{{shout .ID}}", false)
	check("git.branch_template", "{{.ID}} {{.Slug}}", false)
	check("git.branch_template", "{{.Nope}}", false)
	check("git.commit_template", "{{upper .ID}} {{trunc 10 .Title}}", true)
	check("git.commit_template", "{{.ID", false)
	check("git.commit_template", "{{shout .ID}}", false)
	check("git.commit_trailer", "true", true)
	check("git.commit_trailer", "false", true)
	check("git.commit_trailer", "yes", false)
}
