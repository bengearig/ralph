package config

import (
	"strings"
	"testing"
)

func TestDocumentsSettings(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("documents.versions.keep")
	if !ok || s.Default != "0" || s.Scope != ScopeProject || s.Description == "" {
		t.Fatalf("documents.versions.keep = %+v, %v", s, ok)
	}
	for v, valid := range map[string]bool{"0": true, "12": true, "-1": false, "1.5": false, "abc": false, "": false} {
		if err := s.Validate(v); (err == nil) != valid {
			t.Errorf("%q: err = %v, want valid = %v", v, err, valid)
		}
	}
}

func TestExportSettings(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("documents.export.dir")
	if !ok || s.Default != "docs/ralph" || s.Scope != ScopeProject || s.Description == "" {
		t.Fatalf("documents.export.dir = %+v, %v", s, ok)
	}
	for _, v := range []string{"docs/ralph", "a/b/c", "exports"} {
		if p := valueProblem(s, v); p != "" {
			t.Errorf("%q: unexpected problem %q", v, p)
		}
	}
	for _, v := range []string{"", "/abs", "..", "a/../..", ".", "./", ".git", ".git/x", ".ralph", ".ralph/docs", `a\b`, "C:/x", "../x"} {
		if p := valueProblem(s, v); !strings.Contains(p, "documents.export.dir") {
			t.Errorf("%q: problem %q does not name the setting", v, p)
		}
	}
}
