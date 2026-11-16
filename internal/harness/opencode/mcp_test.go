package opencode_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bengearig/ralph/internal/harness"
)

func mcpOf(t *testing.T, arts []harness.Artifact) harness.Artifact {
	t.Helper()
	var found []harness.Artifact
	for _, a := range arts {
		if a.Kind == harness.KindJSONEntry {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d json entries", len(found))
	}
	return found[0]
}

func TestOpenCodeConfigFileChoice(t *testing.T) {
	f := &fstest.MapFile{Data: []byte("{}")}
	dirOnly := fstest.MapFS{"opencode.json/x": f}
	for name, tc := range map[string]struct {
		fs   fstest.MapFS
		want string
	}{
		"none":                   {fstest.MapFS{}, "opencode.json"},
		"json":                   {fstest.MapFS{"opencode.json": f}, "opencode.json"},
		"jsonc":                  {fstest.MapFS{"opencode.jsonc": f}, "opencode.jsonc"},
		"dir json":               {fstest.MapFS{".opencode/opencode.json": f}, ".opencode/opencode.json"},
		"dir jsonc":              {fstest.MapFS{".opencode/opencode.jsonc": f}, ".opencode/opencode.jsonc"},
		"both":                   {fstest.MapFS{"opencode.json": f, "opencode.jsonc": f}, "opencode.json"},
		"directory named":        {dirOnly, "opencode.json"},
		"directory named, jsonc": {fstest.MapFS{"opencode.json/x": f, "opencode.jsonc": f}, "opencode.jsonc"},
	} {
		t.Run(name, func(t *testing.T) {
			in := realInputs(t)
			in.Project = tc.fs
			if got := mcpOf(t, artifacts(t, in)).Path; got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenCodeMCPEntryArtifact(t *testing.T) {
	a := mcpOf(t, artifacts(t, realInputs(t)))
	if a.Kind != harness.KindJSONEntry || a.Scope != harness.ScopeProject {
		t.Fatalf("kind/scope = %v/%v", a.Kind, a.Scope)
	}
	if !reflect.DeepEqual(a.KeyPath, []string{"mcp", "servers", "ralph"}) {
		t.Fatalf("key path = %v", a.KeyPath)
	}
	if a.Label != "MCP server in opencode.json" {
		t.Fatalf("label = %q", a.Label)
	}
	var v struct {
		Command []string `json:"command"`
		Type    string   `json:"type"`
	}
	if err := json.Unmarshal(a.Content, &v); err != nil || v.Type != "local" || !reflect.DeepEqual(v.Command, []string{"ralph", "mcp"}) {
		t.Fatalf("content = %s (%v)", a.Content, err)
	}
	if string(a.Content) != `{"command":["ralph","mcp"],"type":"local"}` {
		t.Fatalf("content = %s", a.Content)
	}
}

func TestOpenCodeMCPEntryUsesInputs(t *testing.T) {
	in := realInputs(t)
	in.MCP = harness.MCPServer{Name: "other", Command: "/bin/r", Args: []string{"mcp", "--x"}}
	a := mcpOf(t, artifacts(t, in))
	if !reflect.DeepEqual(a.KeyPath, []string{"mcp", "servers", "other"}) {
		t.Fatalf("key path = %v", a.KeyPath)
	}
	if string(a.Content) != `{"command":["/bin/r","mcp","--x"],"type":"local"}` {
		t.Fatalf("content = %s", a.Content)
	}
}

func TestOpenCodeArtifactOrderAndKinds(t *testing.T) {
	arts := artifacts(t, realInputs(t))
	var kinds []harness.Kind
	for _, a := range arts {
		if kinds = append(kinds, a.Kind); strings.Contains(a.Path, "permission") {
			t.Fatalf("permission artifact %q", a.Path)
		}
	}
	n := len(kinds)
	if n < 3 || kinds[n-1] != harness.KindJSONEntry || kinds[n-2] != harness.KindSection {
		t.Fatalf("kinds = %v", kinds)
	}
	for _, k := range kinds[:n-2] {
		if k != harness.KindFile {
			t.Fatalf("kinds = %v", kinds)
		}
	}
}
