package config

import "testing"

func scopeRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(
		Setting{Path: "me.token", Default: "dflt", Flag: "token", Scope: ScopeUser},
		Setting{Path: "team.name", Flag: "team"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func scopeFixture(t *testing.T) fixture {
	f := newFixture(t)
	f.reg = scopeRegistry(t)
	return f
}

func mustLoad(t *testing.T, o Options) *Config {
	t.Helper()
	c, err := Load(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestScopeDefaultsToProject(t *testing.T) {
	if (Setting{}).Scope != ScopeProject {
		t.Fatal("zero Scope is not ScopeProject")
	}
}

func TestUserOnlyIgnoredInProjectFile(t *testing.T) {
	f := scopeFixture(t)
	write(t, f.projFile, "team:\n  name: x\nme:\n  token: secret\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("me.token"); v.Value != "dflt" || v.Layer != LayerDefault {
		t.Errorf("default not used: %+v", v)
	}
	w := c.Warnings()
	if len(w) != 1 || w[0] != (Warning{File: f.projFile, Line: 4, Path: "me.token"}) {
		t.Fatalf("warnings = %+v", w)
	}

	write(t, f.userFile, "me:\n  token: fromuser\n")
	c = mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("me.token"); v.Value != "fromuser" || v.Layer != LayerUser {
		t.Errorf("lower layer not used: %+v", v)
	}
	if len(c.Warnings()) != 1 {
		t.Errorf("warnings = %+v", c.Warnings())
	}
}

func TestUserOnlyWarningText(t *testing.T) {
	got := Warning{File: "/r/.ralph/project.yaml", Line: 3, Path: "me.token"}.String()
	want := `/r/.ralph/project.yaml:3: "me.token" is user-only and was ignored in the project file`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUserOnlyAppliedFromUserFile(t *testing.T) {
	f := scopeFixture(t)
	write(t, f.userFile, "me:\n  token: u\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("me.token"); v.Value != "u" || v.Layer != LayerUser {
		t.Errorf("got %+v", v)
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("warnings = %+v", c.Warnings())
	}
}

func TestUserOnlyAppliedFromProjectLocalFile(t *testing.T) {
	f := scopeFixture(t)
	write(t, f.localFi, "me:\n  token: l\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("me.token"); v.Value != "l" || v.Layer != LayerProjectLocal {
		t.Errorf("got %+v", v)
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("warnings = %+v", c.Warnings())
	}
}

func TestUserOnlyAppliedFromEnv(t *testing.T) {
	f := scopeFixture(t)
	c := mustLoad(t, f.opts(map[string]string{"RALPH_ME_TOKEN": "e"}, nil))
	if v, _ := c.Get("me.token"); v.Value != "e" || v.Layer != LayerEnv {
		t.Errorf("got %+v", v)
	}
}

func TestUserOnlyAppliedFromFlag(t *testing.T) {
	f := scopeFixture(t)
	c := mustLoad(t, f.opts(nil, map[string]string{"token": "g"}))
	if v, _ := c.Get("me.token"); v.Value != "g" || v.Layer != LayerFlag {
		t.Errorf("got %+v", v)
	}
}

func TestProjectAllowedStillAppliedInProjectFile(t *testing.T) {
	f := scopeFixture(t)
	write(t, f.projFile, "team:\n  name: x\n")
	c := mustLoad(t, f.opts(nil, nil))
	if v, _ := c.Get("team.name"); v.Value != "x" || v.Layer != LayerProject {
		t.Errorf("got %+v", v)
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("warnings = %+v", c.Warnings())
	}
}

func TestScopeUnknownKeyStillError(t *testing.T) {
	f := scopeFixture(t)
	write(t, f.projFile, "me:\n  nope: 1\n")
	if _, err := Load(f.opts(nil, nil)); err == nil {
		t.Fatal("expected error")
	}
}
