package harness

import (
	"strings"
	"testing"
)

func TestArtifactValidateTable(t *testing.T) {
	file := Artifact{Kind: KindFile, Scope: ScopeProject, Path: "a/b.md", Content: []byte("x")}
	section := Artifact{Kind: KindSection, Scope: ScopeProject, Path: "NOTES.md", Content: []byte("body")}
	entry := Artifact{Kind: KindJSONEntry, Scope: ScopeProject, Path: "x.json", KeyPath: []string{"a", "b"}, Content: []byte(`{"k":1}`)}
	item := Artifact{Kind: KindJSONArrayItem, Scope: ScopeLocal, Path: "y.json", KeyPath: []string{"p", "allow"}, Content: []byte(`"v"`)}

	with := func(a Artifact, f func(*Artifact)) Artifact { f(&a); return a }

	tests := []struct {
		name    string
		art     Artifact
		wantErr string
	}{
		{"file", file, ""},
		{"section", section, ""},
		{"json entry", entry, ""},
		{"json array item", item, ""},
		{"empty path", with(file, func(a *Artifact) { a.Path = "" }), "empty path"},
		{"absolute path", with(file, func(a *Artifact) { a.Path = "/etc/x" }), "not relative"},
		{"dotdot segment", with(file, func(a *Artifact) { a.Path = "a/../../b" }), ".."},
		{"leading dotdot", with(file, func(a *Artifact) { a.Path = "../b" }), ".."},
		{"backslash", with(file, func(a *Artifact) { a.Path = `a\b` }), "backslash"},
		{"empty content", with(file, func(a *Artifact) { a.Content = nil }), "empty content"},
		{"unknown kind", with(file, func(a *Artifact) { a.Kind = "other" }), "unknown kind"},
		{"unknown scope", with(file, func(a *Artifact) { a.Scope = "other" }), "unknown scope"},
		{"section marker", with(section, func(a *Artifact) { a.Content = []byte("a <!-- ralph:begin --> b") }), "marker"},
		{"entry without key path", with(entry, func(a *Artifact) { a.KeyPath = nil }), "key path"},
		{"item without key path", with(item, func(a *Artifact) { a.KeyPath = nil }), "key path"},
		{"empty key element", with(entry, func(a *Artifact) { a.KeyPath = []string{"a", ""} }), "empty element"},
		{"invalid json", with(entry, func(a *Artifact) { a.Content = []byte("{") }), "not valid JSON"},
		{"file with key path", with(file, func(a *Artifact) { a.KeyPath = []string{"a"} }), "key path"},
		{"section with key path", with(section, func(a *Artifact) { a.KeyPath = []string{"a"} }), "key path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.art.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
			if tt.art.Path != "" && !strings.Contains(err.Error(), tt.art.Path) {
				t.Errorf("error %q does not name the path %q", err, tt.art.Path)
			}
		})
	}
}

func TestArtifactIDsStableAndUnique(t *testing.T) {
	file := Artifact{Kind: KindFile, Path: "a.md", Content: []byte("x")}
	section := Artifact{Kind: KindSection, Path: "a.md", Content: []byte("x")}
	entry := Artifact{Kind: KindJSONEntry, Path: "m.json", KeyPath: []string{"mcpServers", "ralph"}, Content: []byte("{}")}
	itemA := Artifact{Kind: KindJSONArrayItem, Path: "s.json", KeyPath: []string{"permissions", "allow"}, Content: []byte(`"a"`)}
	itemB := itemA
	itemB.Content = []byte(`"b"`)
	spaced := itemA
	spaced.Content = []byte(`{ "k": 1 }`)
	compact := itemA
	compact.Content = []byte(`{"k":1}`)

	want := map[string]Artifact{
		"file:a.md":                                 file,
		"section:a.md":                              section,
		"json:m.json#mcpServers.ralph":              entry,
		`jsonitem:s.json#permissions.allow="a"`:     itemA,
		`jsonitem:s.json#permissions.allow="b"`:     itemB,
		`jsonitem:s.json#permissions.allow={"k":1}`: spaced,
	}
	for id, a := range want {
		if got := a.ID(); got != id {
			t.Errorf("ID() = %q, want %q", got, id)
		}
		if got := a.ID(); got != id {
			t.Errorf("ID() not stable: %q", got)
		}
	}
	if itemA.ID() == itemB.ID() {
		t.Error("array items with different values share an ID")
	}
	if spaced.ID() != compact.ID() {
		t.Error("equivalent JSON items differ in ID")
	}
}
