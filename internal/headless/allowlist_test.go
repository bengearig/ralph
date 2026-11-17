package headless

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
)

func TestSimpleCommandsSplitsOperators(t *testing.T) {
	for in, want := range map[string][]string{
		"a && b || c; d | e & f":   {"a", "b", "c", "d", "e", "f"},
		"a\nb\r\n\n c ":            {"a", "b", "c"},
		" ; && | ":                 {},
		"":                         {},
		`echo "a && b"`:            {`echo "a`, `b"`},
		"go test ./... -run 'A|B'": {"go test ./... -run 'A", "B'"},
		"make lint PKGS=./x/...":   {"make lint PKGS=./x/..."},
		"a &&& b ||| c;;; d":       {"a", "b", "c", "d"},
	} {
		if got := SimpleCommands(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SimpleCommands(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapePatternEscapesStarAndBackslash(t *testing.T) {
	for in, want := range map[string]string{
		"go test ./...": "go test ./...",
		"a*b":           `a\*b`,
		`a\b`:           `a\\b`,
		`\*`:            `\\\*`,
		"":              "",
	} {
		if got := EscapePattern(in); got != want {
			t.Errorf("EscapePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRestrictedBaselineAndOrder(t *testing.T) {
	a := Restricted([]string{"go build ./... && go test ./...", "make lint"}, []string{"npm test *", "ls"})
	want := append(slices.Clone(ReadOnlyGit),
		"go build ./... && go test ./...", "go build ./...", "go test ./...",
		"make lint",
		"npm test *", "ls")
	if !reflect.DeepEqual(a.Commands, want) {
		t.Errorf("Commands =\n%q\nwant\n%q", a.Commands, want)
	}
	if !reflect.DeepEqual(a.Tools, ReadOnlyTools) {
		t.Errorf("Tools = %q", a.Tools)
	}
	if got := Restricted(nil, nil).Commands; !reflect.DeepEqual(got, ReadOnlyGit) {
		t.Errorf("baseline Commands = %q", got)
	}
}

func TestRestrictedDedupes(t *testing.T) {
	a := Restricted([]string{"make lint", "make lint && make lint"}, []string{"git status *", "make lint", "x", "x"})
	want := append(slices.Clone(ReadOnlyGit), "make lint", "make lint && make lint", "x")
	if !reflect.DeepEqual(a.Commands, want) {
		t.Errorf("Commands = %q, want %q", a.Commands, want)
	}
}

func TestRestrictedProtectedPaths(t *testing.T) {
	if got, want := Restricted(nil, nil).Protected, []string{".git/", ".ralph/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Protected = %q, want %q", got, want)
	}
}

func TestRestrictedVerifyCommandWithStarMatchesOnlyItself(t *testing.T) {
	a := Restricted([]string{"rm -rf *"}, nil)
	if !slices.Contains(a.Commands, `rm -rf \*`) || slices.Contains(a.Commands, "rm -rf *") {
		t.Errorf("Commands = %q", a.Commands)
	}
}

func TestNewRequestFromConfig(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(t.TempDir(), "user.yaml")
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    user,
		ProjectRoot: root,
		Getenv: func(k string) (string, bool) {
			switch k {
			case "RALPH_VERIFY_COMMANDS":
				return `["go test ./..."]`, true
			case "RALPH_HEADLESS_ALLOW":
				return `["npm run build *"]`, true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp := harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}
	req, err := NewRequest(cfg, root, "do it", mcp)
	if err != nil {
		t.Fatal(err)
	}
	if req.Dir != root || req.Prompt != "do it" || req.MCP.Name != "ralph" || req.Mode != harness.PermissionRestricted {
		t.Errorf("req = %+v", req)
	}
	want := append(slices.Clone(ReadOnlyGit), "go test ./...", "npm run build *")
	if !reflect.DeepEqual(req.Allow.Commands, want) {
		t.Errorf("Commands = %q, want %q", req.Allow.Commands, want)
	}
	if _, err := NewRequest(cfg, "relative", "x", mcp); err == nil {
		t.Error("relative root accepted")
	}
}
