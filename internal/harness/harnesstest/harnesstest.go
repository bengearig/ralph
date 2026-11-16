// Package harnesstest holds the conformance check every adapter must pass.
package harnesstest

import (
	"bytes"
	"context"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/workflow"
)

const detectTimeout = 5 * time.Second

type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// SampleInputs is what Check renders adapters from.
func SampleInputs() harness.Inputs {
	return harness.Inputs{
		Instructions: []workflow.Instruction{
			{Name: "status", Description: "Show project status", ArgumentHint: "[unit id]", Body: "Call the status tool and summarize it."},
			{Name: "search", Description: "Search the plan", ArgumentHint: "<question>", Body: "Call the search tool with the question."},
		},
		ProjectNotes: "ralph keeps the plan; ask it before guessing.",
		MCP:          harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
	}
}

// Check runs every conformance check against a.
func Check(t *testing.T, a harness.Adapter) {
	t.Helper()
	check(t, a)
}

// CheckDeterministic fails if two Artifacts calls differ.
func CheckDeterministic(t *testing.T, a harness.Adapter) {
	t.Helper()
	checkDeterministic(t, a, SampleInputs())
}

func check(r reporter, a harness.Adapter) {
	r.Helper()
	name := a.Name()
	if name == "" || name != strings.ToLower(name) || strings.ContainsAny(name, " \t\n") {
		r.Errorf("adapter name %q must be non-empty, lowercase and contain no spaces", name)
	}

	in := SampleInputs()
	arts, err := a.Artifacts(in)
	if err != nil {
		r.Errorf("Artifacts returned an error: %v", err)
		return
	}

	seen := map[string]bool{}
	for _, art := range arts {
		if err := art.Validate(); err != nil {
			r.Errorf("invalid artifact: %v", err)
		}
		id := art.ID()
		if seen[id] {
			r.Errorf("duplicate artifact ID %q", id)
		}
		seen[id] = true
		if escapes(art.Path) {
			r.Errorf("artifact path %q escapes the project", art.Path)
		}
		if art.Kind == harness.KindSection && bytes.Contains(art.Content, []byte("<!-- ralph:")) {
			r.Errorf("section %s carries a marker", art.Path)
		}
	}

	checkDeterministic(r, a, in)
	checkDetect(r, a)
}

func escapes(p string) bool {
	if p == "" || path.IsAbs(p) || strings.Contains(p, `\`) {
		return true
	}
	c := path.Clean(p)
	return c == ".." || strings.HasPrefix(c, "../") || strings.Contains("/"+p+"/", "/../")
}

func checkDeterministic(r reporter, a harness.Adapter, in harness.Inputs) {
	r.Helper()
	first, err1 := a.Artifacts(in)
	second, err2 := a.Artifacts(in)
	if (err1 == nil) != (err2 == nil) {
		r.Errorf("Artifacts is not deterministic: errors differ (%v, %v)", err1, err2)
		return
	}
	if !reflect.DeepEqual(first, second) {
		r.Errorf("Artifacts is not deterministic: two calls returned different results")
	}
}

func checkDetect(r reporter, a harness.Adapter) {
	r.Helper()
	dir, err := tempDir()
	if err != nil {
		r.Errorf("creating temporary directory: %v", err)
		return
	}
	defer removeAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		a.Detect(ctx, dir)
	}()
	select {
	case p := <-done:
		if p != nil {
			r.Errorf("Detect panicked: %v", p)
		}
	case <-time.After(detectTimeout):
		r.Errorf("Detect did not return within %s", detectTimeout)
	}
}
