package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func listRegistry(t *testing.T, scope Scope) *Registry {
	t.Helper()
	r, err := NewRegistry(
		Setting{
			Path: "verify.commands", Kind: KindList, Scope: scope,
			ValidateItem: func(v string) error {
				if v == "bad" {
					return errors.New("is not allowed")
				}
				return nil
			},
		},
		Setting{Path: "plain", Default: "x"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func listFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	f.reg = listRegistry(t, ScopeProject)
	return f
}

func loadProblems(t *testing.T, f fixture, env map[string]string) []Problem {
	t.Helper()
	_, err := Load(f.opts(env, nil))
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *Error", err)
	}
	return ce.Problems
}

func TestRegistryListRejectsDefaultFlagValidateAllowed(t *testing.T) {
	noop := func(string) error { return nil }
	for name, s := range map[string]Setting{
		"default":  {Path: "a.list", Kind: KindList, Default: `["x"]`},
		"flag":     {Path: "a.list", Kind: KindList, Flag: "list"},
		"validate": {Path: "a.list", Kind: KindList, Validate: noop},
		"allowed":  {Path: "a.list", Kind: KindList, Allowed: []string{"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRegistry(s)
			if err == nil || !strings.Contains(err.Error(), "a.list") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRegistryItemValidatorOnlyForLists(t *testing.T) {
	_, err := NewRegistry(Setting{Path: "a.str", ValidateItem: func(string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "a.str") {
		t.Fatalf("err = %v", err)
	}
}

func TestListFromProjectFile(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "verify:\n  commands:\n    - go test ./...\n    - go vet ./...\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.List("verify.commands")
	if !ok || !slices.Equal(got, []string{"go test ./...", "go vet ./..."}) {
		t.Fatalf("List = %q, %v", got, ok)
	}
	v, _ := cfg.Get("verify.commands")
	if v.Value != `["go test ./...","go vet ./..."]` || v.Layer != LayerProject || v.Source.Line != 3 {
		t.Errorf("value = %+v", v)
	}
}

func TestListEmptySequenceIsSet(t *testing.T) {
	f := listFixture(t)
	write(t, f.userFile, "verify:\n  commands: [a]\n")
	write(t, f.projFile, "verify:\n  commands: []\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.List("verify.commands")
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("List = %#v, %v", got, ok)
	}
	if v, _ := cfg.Get("verify.commands"); v.Value != "[]" {
		t.Errorf("value = %q", v.Value)
	}
}

func TestListLaterLayerReplaces(t *testing.T) {
	f := listFixture(t)
	write(t, f.userFile, "verify:\n  commands: [u1, u2]\n")
	check := func(want ...string) {
		t.Helper()
		cfg, err := Load(f.opts(f.env, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := cfg.List("verify.commands"); !slices.Equal(got, want) {
			t.Fatalf("List = %q, want %q", got, want)
		}
	}
	check("u1", "u2")
	write(t, f.projFile, "verify:\n  commands: [p1]\n")
	check("p1")
	write(t, f.localFi, "verify:\n  commands: [l1, l2, l3]\n")
	check("l1", "l2", "l3")
	f.env = map[string]string{"RALPH_VERIFY_COMMANDS": `[ "e1" ]`}
	check("e1")
}

func TestListNotSetReturnsFalse(t *testing.T) {
	f := listFixture(t)
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cfg.List("verify.commands"); ok || got != nil {
		t.Errorf("List = %#v, %v", got, ok)
	}
}

func TestListOnNonListSettingReturnsFalse(t *testing.T) {
	f := listFixture(t)
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"plain", "missing"} {
		if got, ok := cfg.List(p); ok || got != nil {
			t.Errorf("List(%q) = %#v, %v", p, got, ok)
		}
	}
}

func TestListFileProblems(t *testing.T) {
	const msg = `setting "verify.commands" must be a list of text values`
	for name, body := range map[string]string{
		"scalar":          "verify:\n  commands: go test\n",
		"mapping":         "verify:\n  commands:\n    a: b\n",
		"null":            "verify:\n  commands:\n",
		"null item":       "verify:\n  commands:\n    - a\n    - null\n",
		"nested sequence": "verify:\n  commands:\n    - [a]\n",
		"mapping item":    "verify:\n  commands:\n    - a: b\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := listFixture(t)
			write(t, f.projFile, body)
			ps := loadProblems(t, f, nil)
			want := Problem{File: f.projFile, Line: 2, Message: msg}
			if len(ps) != 1 || ps[0] != want {
				t.Fatalf("problems = %+v, want %+v", ps, want)
			}
		})
	}
}

func TestListSequenceForStringSettingKeepsOldProblem(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "plain: [a]\n")
	ps := loadProblems(t, f, nil)
	want := Problem{File: f.projFile, Line: 1, Message: `setting "plain" must be a scalar value`}
	if len(ps) != 1 || ps[0] != want {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestListEnvJSON(t *testing.T) {
	f := listFixture(t)
	cfg, err := Load(f.opts(map[string]string{"RALPH_VERIFY_COMMANDS": `["a", "b c"]`}, nil))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := cfg.Get("verify.commands")
	if v.Value != `["a","b c"]` || v.Layer != LayerEnv || v.Source.Name != "RALPH_VERIFY_COMMANDS" {
		t.Errorf("value = %+v", v)
	}
	for _, bad := range []string{"go test", `{"a":"b"}`, `["a",1]`, `null`, `["a"] x`, `[null]`} {
		ps := loadProblems(t, f, map[string]string{"RALPH_VERIFY_COMMANDS": bad})
		want := Problem{Source: "RALPH_VERIFY_COMMANDS", Message: "must be a JSON array of strings"}
		if len(ps) != 1 || ps[0] != want {
			t.Errorf("%q: problems = %+v", bad, ps)
		}
	}
}

func TestListEmptyEnvIgnored(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "verify:\n  commands: [p]\n")
	cfg, err := Load(f.opts(map[string]string{"RALPH_VERIFY_COMMANDS": ""}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.List("verify.commands"); !slices.Equal(got, []string{"p"}) {
		t.Errorf("List = %q", got)
	}
}

func TestListItemRules(t *testing.T) {
	many := make([]string, 51)
	for i := range many {
		many[i] = "c"
	}
	for name, tc := range map[string]struct {
		items []string
		want  string
	}{
		"blank":      {[]string{"a", " \t"}, "item 2: must not be blank"},
		"long":       {[]string{strings.Repeat("x", 4097)}, "item 1: must be at most 4096 bytes"},
		"nul":        {[]string{"a\x00b"}, "item 1: must not contain a NUL byte"},
		"too many":   {many, "at most 50 items"},
		"item check": {[]string{"ok", "bad"}, "item 2: is not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			f := listFixture(t)
			enc, err := json.Marshal(tc.items)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"RALPH_VERIFY_COMMANDS": string(enc)}
			ps := loadProblems(t, f, env)
			if len(ps) != 1 || !strings.Contains(ps[0].Message, tc.want) || !strings.Contains(ps[0].Message, "verify.commands") {
				t.Fatalf("problems = %+v, want %q", ps, tc.want)
			}
			if name == "nul" {
				return
			}
			var yml strings.Builder
			yml.WriteString("verify:\n  commands:\n")
			for _, it := range tc.items {
				fmt.Fprintf(&yml, "    - %q\n", it)
			}
			write(t, f.projFile, yml.String())
			ps = loadProblems(t, f, nil)
			if len(ps) != 1 || ps[0].File != f.projFile || !strings.Contains(ps[0].Message, tc.want) {
				t.Fatalf("file problems = %+v, want %q", ps, tc.want)
			}
		})
	}
	f := listFixture(t)
	if _, err := Load(f.opts(map[string]string{"RALPH_VERIFY_COMMANDS": string(mustJSON(t, many[:50]))}, nil)); err != nil {
		t.Errorf("50 items rejected: %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestListReturnsCopy(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "verify:\n  commands: [a, b]\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := cfg.List("verify.commands")
	first[0] = "changed"
	second, _ := cfg.List("verify.commands")
	if !slices.Equal(second, []string{"a", "b"}) {
		t.Errorf("second = %q", second)
	}
}

func TestListScopeUserIgnoredInProjectFile(t *testing.T) {
	f := newFixture(t)
	f.reg = listRegistry(t, ScopeUser)
	write(t, f.projFile, "verify:\n  commands: [a]\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Get("verify.commands"); ok {
		t.Error("value set from project file")
	}
	if w := cfg.Warnings(); len(w) != 1 || w[0].Path != "verify.commands" || w[0].Line != 2 {
		t.Errorf("warnings = %+v", w)
	}
}

func TestShowListText(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "verify:\n  commands: [a, 'b c']\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, cfg, f.root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `["a","b c"]`) {
		t.Errorf("output:\n%s", buf.String())
	}
}

func TestShowListJSON(t *testing.T) {
	f := listFixture(t)
	write(t, f.projFile, "verify:\n  commands: [a, 'b c']\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, cfg); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Setting string          `json:"setting"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		switch e.Setting {
		case "verify.commands":
			var items []string
			if err := json.Unmarshal(e.Value, &items); err != nil || !slices.Equal(items, []string{"a", "b c"}) {
				t.Errorf("list value = %s (%v)", e.Value, err)
			}
		case "plain":
			var s string
			if err := json.Unmarshal(e.Value, &s); err != nil || s != "x" {
				t.Errorf("plain value = %s (%v)", e.Value, err)
			}
		}
	}
}
