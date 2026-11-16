package config

import (
	"path/filepath"
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
