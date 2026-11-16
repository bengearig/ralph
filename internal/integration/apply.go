package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/project"
)

// Action says what Apply did for one artifact.
type Action string

const (
	ActionInstalled Action = "installed"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionAdopted   Action = "adopted"
	ActionKept      Action = "kept"
	ActionSkipped   Action = "skipped"
)

// Result reports one artifact.
type Result struct {
	ID     string
	Path   string
	Label  string
	Action Action
	Detail string
}

type scopeManifest struct {
	path     string
	manifest Manifest
	dirty    bool
}

// state is what an artifact's file holds compared with what ralph would write.
type state struct {
	present bool
	current string
	desired string
	problem string
	build   func() ([]byte, error)
}

// Apply installs the artifacts under root, never overwriting a file ralph did
// not write. Results are returned for the artifacts handled before any error.
func Apply(ctx context.Context, root, adapterName string, artifacts []harness.Artifact, ralphVersion string) (results []Result, err error) {
	for _, a := range artifacts {
		if err := a.Validate(); err != nil {
			return nil, err
		}
	}
	dir := project.RalphDir(root)
	scopes := map[harness.Scope]*scopeManifest{}
	for scope, file := range map[harness.Scope]string{
		harness.ScopeProject: project.IntegrationFile,
		harness.ScopeLocal:   project.IntegrationLocalFile,
	} {
		path := filepath.Join(dir, file)
		m, err := LoadManifest(path)
		if err != nil {
			return nil, err
		}
		scopes[scope] = &scopeManifest{path: path, manifest: m}
	}
	var customized []string
	for _, s := range scopes {
		customized = append(customized, s.manifest.Customized...)
	}

	defer func() {
		for _, s := range scopes {
			if !s.dirty {
				continue
			}
			if serr := s.manifest.Save(s.path); serr != nil {
				err = errors.Join(err, serr)
			}
		}
	}()

	for _, a := range artifacts {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		id := a.ID()
		res := Result{ID: id, Path: a.Path, Label: a.Label}
		if slices.Contains(customized, a.Path) || slices.Contains(customized, id) {
			res.Action = ActionSkipped
			results = append(results, res)
			continue
		}
		sm := scopes[a.Scope]
		res, err = applyOne(root, adapterName, ralphVersion, a, sm, res)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

func applyOne(root, adapterName, ralphVersion string, a harness.Artifact, sm *scopeManifest, res Result) (Result, error) {
	abs := filepath.Join(root, filepath.FromSlash(a.Path))
	target := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		target = resolved
	}
	info, statErr := os.Stat(target)
	var data []byte
	exists := false
	switch {
	case statErr == nil && info.IsDir():
		res.Action = ActionKept
		res.Detail = "path is a directory; left unchanged"
		return res, nil
	case statErr == nil:
		exists = true
		b, err := os.ReadFile(target)
		if err != nil {
			return res, fmt.Errorf("integration: read %s: %w", a.Path, err)
		}
		data = b
	case !errors.Is(statErr, os.ErrNotExist):
		return res, fmt.Errorf("integration: stat %s: %w", a.Path, statErr)
	}

	if exists && isJSONC(a) {
		return applyJSONC(a, data, sm, adapterName, ralphVersion, res)
	}

	st := inspect(a, data, exists)
	if st.problem != "" {
		res.Action = ActionKept
		res.Detail = st.problem + "; left unchanged"
		return res, nil
	}

	entry, recorded := sm.manifest.find(a.ID())
	newEntry := Entry{
		ID: a.ID(), Kind: string(a.Kind), Path: a.Path, KeyPath: a.KeyPath,
		SHA256: st.desired, Adapter: adapterName, Ralph: ralphVersion,
	}
	record := func() {
		sm.manifest.record(newEntry)
		sm.dirty = true
	}

	switch {
	case !st.present:
		if err := write(st, target, info, exists, &res, "cannot install"); err != nil || res.Action == ActionKept {
			return res, err
		}
		record()
		res.Action = ActionInstalled
	case st.current == st.desired:
		if recorded && entry.SHA256 == st.current {
			res.Action = ActionUnchanged
			return res, nil
		}
		record()
		res.Action = ActionAdopted
	case recorded && entry.SHA256 == st.current:
		if err := write(st, target, info, exists, &res, "cannot update"); err != nil || res.Action == ActionKept {
			return res, err
		}
		record()
		res.Action = ActionUpdated
	default:
		res.Action = ActionKept
		res.Detail = "differs from what ralph would write; left unchanged"
		if a.Kind == harness.KindFile {
			newPath := target + ".new"
			if err := writeAtomic(newPath, a.Content, 0o644); err != nil {
				return res, err
			}
			res.Detail = fmt.Sprintf("differs from what ralph would write; new version written to %s.new", a.Path)
		}
	}
	return res, nil
}

// write applies the artifact; an edit that cannot be made leaves the file
// alone and marks res kept.
func write(st state, target string, info os.FileInfo, exists bool, res *Result, what string) error {
	out, err := st.build()
	if err != nil {
		res.Action = ActionKept
		res.Detail = fmt.Sprintf("%s: %v; left unchanged", what, err)
		return nil
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("integration: create %s: %w", filepath.Dir(target), err)
	}
	return writeAtomic(target, out, mode)
}

func inspect(a harness.Artifact, data []byte, exists bool) state {
	var st state
	switch a.Kind {
	case harness.KindSection:
		st.desired = SectionHash(string(a.Content))
		if exists {
			sec, err := FindSection(data)
			if err != nil {
				st.problem = fmt.Sprintf("markers in %s are malformed: %v", a.Path, err)
				return st
			}
			st.present = sec.Found
			if sec.Found {
				st.current = SectionHash(sec.Content)
			}
		}
		st.build = func() ([]byte, error) { return ReplaceSection(data, string(a.Content)) }
	case harness.KindJSONEntry, harness.KindJSONArrayItem:
		st.desired = CanonicalHash(a.Content)
		doc := data
		if len(bytes.TrimSpace(doc)) == 0 {
			doc = []byte("{}")
		}
		if a.Kind == harness.KindJSONEntry {
			v, ok, err := GetPath(doc, a.KeyPath)
			if err != nil {
				st.problem = fmt.Sprintf("%s cannot be edited: %v", a.Path, err)
				return st
			}
			st.present = ok
			if ok {
				st.current = CanonicalHash(v)
			}
			st.build = func() ([]byte, error) { return SetPath(doc, a.KeyPath, a.Content) }
		} else {
			has, err := HasArrayItem(doc, a.KeyPath, a.Content)
			if err != nil {
				st.problem = fmt.Sprintf("%s cannot be edited: %v", a.Path, err)
				return st
			}
			st.present = has
			if has {
				st.current = st.desired
			}
			st.build = func() ([]byte, error) {
				out, _, err := EnsureArrayItem(doc, a.KeyPath, a.Content)
				return out, err
			}
		}
	default:
		sum := sha256.Sum256(a.Content)
		st.desired = hex.EncodeToString(sum[:])
		if exists {
			cur := sha256.Sum256(data)
			st.present = true
			st.current = hex.EncodeToString(cur[:])
		}
		st.build = func() ([]byte, error) { return a.Content, nil }
	}
	return st
}

func isJSONC(a harness.Artifact) bool {
	return (a.Kind == harness.KindJSONEntry || a.Kind == harness.KindJSONArrayItem) &&
		strings.HasSuffix(strings.ToLower(a.Path), ".jsonc")
}

// applyJSONC never writes: a JSONC file may hold comments an edit would lose.
func applyJSONC(a harness.Artifact, data []byte, sm *scopeManifest, adapterName, ralphVersion string, res Result) (Result, error) {
	doc := data
	if len(bytes.TrimSpace(doc)) == 0 {
		doc = []byte("{}")
	}
	desired := CanonicalHash(a.Content)
	var present bool
	var current string
	if a.Kind == harness.KindJSONEntry {
		v, ok, err := GetPathJSONC(doc, a.KeyPath)
		if err != nil {
			res.Action = ActionKept
			res.Detail = fmt.Sprintf("%s cannot be read: %v; left unchanged", a.Path, err)
			return res, nil
		}
		present = ok
		if ok {
			current = CanonicalHash(v)
		}
	} else {
		has, err := HasArrayItemJSONC(doc, a.KeyPath, a.Content)
		if err != nil {
			res.Action = ActionKept
			res.Detail = fmt.Sprintf("%s cannot be read: %v; left unchanged", a.Path, err)
			return res, nil
		}
		present = has
		if has {
			current = desired
		}
	}
	if present && current == desired {
		entry, recorded := sm.manifest.find(a.ID())
		if recorded && entry.SHA256 == current {
			res.Action = ActionUnchanged
			return res, nil
		}
		sm.manifest.record(Entry{
			ID: a.ID(), Kind: string(a.Kind), Path: a.Path, KeyPath: a.KeyPath,
			SHA256: desired, Adapter: adapterName, Ralph: ralphVersion,
		})
		sm.dirty = true
		res.Action = ActionAdopted
		return res, nil
	}
	value := a.Content
	if v, err := canonicalBytes(a.Content); err == nil {
		value = v
	}
	verb := fmt.Sprintf("set %s to %s", pathName(a.KeyPath), value)
	if a.Kind == harness.KindJSONArrayItem {
		verb = fmt.Sprintf("add %s to %s", value, pathName(a.KeyPath))
	}
	res.Action = ActionKept
	res.Detail = fmt.Sprintf("%s may contain comments, so ralph does not edit it; %s", a.Path, verb)
	return res, nil
}
