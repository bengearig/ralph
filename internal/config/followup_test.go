package config

import (
	"strings"
	"testing"
)

func TestFollowUpDeleteBranchesSetting(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("follow_up.delete_branches")
	if !ok || s.Default != "local" || s.Scope != ScopeProject {
		t.Fatalf("setting = %+v, %v", s, ok)
	}
	for _, w := range []string{"local", "both", "tip"} {
		if !strings.Contains(s.Description, w) {
			t.Errorf("description lacks %q: %s", w, s.Description)
		}
	}
	for v, want := range map[string]bool{"local": true, "both": true, "none": true, "remote": false, "": false} {
		if p := valueProblem(s, v); (p == "") != want {
			t.Errorf("%q: problem %q, want accepted %v", v, p, want)
		}
	}
}
