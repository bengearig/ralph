package config

import "testing"

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
