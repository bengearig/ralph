// Package harnesstest holds the conformance check every adapter must pass.
package harnesstest

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
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

type view struct {
	name string
	fsys fs.FS
}

func file(name string) fstest.MapFile { return fstest.MapFile{Data: []byte("{}\n" + name)} }

// views are the project views every adapter is exercised against; the first
// is a nil view.
func views() []view {
	only := func(names ...string) fs.FS {
		m := fstest.MapFS{}
		for _, n := range names {
			f := file(n)
			m[n] = &f
		}
		return m
	}
	return []view{
		{"nil", nil},
		{"empty", only()},
		{"CLAUDE.md", only("CLAUDE.md")},
		{"AGENTS.md", only("AGENTS.md")},
		{"CLAUDE.md and AGENTS.md", only("CLAUDE.md", "AGENTS.md")},
		{"opencode.json", only("opencode.json")},
		{"opencode.jsonc", only("opencode.jsonc")},
		{".opencode/opencode.json", only(".opencode/opencode.json")},
	}
}

// artifactsOf calls Artifacts and turns a panic into an error.
func artifactsOf(a harness.Adapter, in harness.Inputs) (arts []harness.Artifact, err error) {
	defer func() {
		if p := recover(); p != nil {
			arts, err = nil, fmt.Errorf("Artifacts panicked: %v", p)
		}
	}()
	return a.Artifacts(in)
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

// CheckAll fails if adapters disagree about a shared artifact or claim one path
// with different kinds, for every project view Check uses.
func CheckAll(t *testing.T, adapters []harness.Adapter) {
	t.Helper()
	checkAll(t, adapters)
}

func check(r reporter, a harness.Adapter) {
	r.Helper()
	name := a.Name()
	if name == "" || name != strings.ToLower(name) || strings.ContainsAny(name, " \t\n") {
		r.Errorf("adapter name %q must be non-empty, lowercase and contain no spaces", name)
	}

	for _, v := range views() {
		in := SampleInputs()
		in.Project = v.fsys
		checkView(r, a, in, v.name)
	}
	checkDetect(r, a)
}

func checkView(r reporter, a harness.Adapter, in harness.Inputs, viewName string) {
	r.Helper()
	arts, err := artifactsOf(a, in)
	if err != nil {
		r.Errorf("project %s: Artifacts returned an error: %v", viewName, err)
		return
	}

	seen := map[string]bool{}
	for _, art := range arts {
		if err := art.Validate(); err != nil {
			r.Errorf("project %s: invalid artifact: %v", viewName, err)
		}
		id := art.ID()
		if seen[id] {
			r.Errorf("project %s: duplicate artifact ID %q", viewName, id)
		}
		seen[id] = true
		if escapes(art.Path) {
			r.Errorf("project %s: artifact path %q escapes the project", viewName, art.Path)
		}
		if art.Kind == harness.KindSection && bytes.Contains(art.Content, []byte("<!-- ralph:")) {
			r.Errorf("project %s: section %s carries a marker", viewName, art.Path)
		}
	}

	checkDeterministic(r, a, in)
}

func isJSONKind(k harness.Kind) bool {
	return k == harness.KindJSONEntry || k == harness.KindJSONArrayItem
}

func checkAll(r reporter, adapters []harness.Adapter) {
	r.Helper()
	for _, v := range views() {
		in := SampleInputs()
		in.Project = v.fsys
		byID := map[string]harness.Artifact{}
		idOwner := map[string]string{}
		byPath := map[string]harness.Artifact{}
		pathOwner := map[string]string{}
		for _, a := range adapters {
			arts, err := artifactsOf(a, in)
			if err != nil {
				r.Errorf("project %s: %s: %v", v.name, a.Name(), err)
				continue
			}
			for _, art := range arts {
				id := art.ID()
				if prev, ok := byID[id]; ok {
					if idOwner[id] != a.Name() && !sameArtifact(prev, art) {
						r.Errorf("project %s: %s and %s produce artifact %q with different content", v.name, idOwner[id], a.Name(), id)
					}
				} else {
					byID[id] = art
					idOwner[id] = a.Name()
				}
				if prev, ok := byPath[art.Path]; ok {
					if prev.Kind != art.Kind && (!isJSONKind(prev.Kind) || !isJSONKind(art.Kind)) {
						r.Errorf("project %s: %s claims %s as %s and %s as %s", v.name, pathOwner[art.Path], art.Path, prev.Kind, a.Name(), art.Kind)
					}
				} else {
					byPath[art.Path] = art
					pathOwner[art.Path] = a.Name()
				}
			}
		}
	}
}

func sameArtifact(a, b harness.Artifact) bool {
	return a.Kind == b.Kind && a.Scope == b.Scope && a.Path == b.Path &&
		slicesEqual(a.KeyPath, b.KeyPath) && bytes.Equal(a.Content, b.Content)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	first, err1 := artifactsOf(a, in)
	second, err2 := artifactsOf(a, in)
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
