package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/workflow"
)

// Outdated is an artifact ralph wrote, the user has not changed, and a newer
// ralph would write differently.
type Outdated struct {
	ID    string
	Path  string
	Label string
}

// InputsFor returns what adapters render their artifacts from for the project
// at root, with the project's workflow overrides applied.
func InputsFor(root string) (harness.Inputs, error) {
	in, _, err := InputsWithOverrides(root)
	return in, err
}

// InputsWithOverrides is InputsFor that also returns the workflow overrides
// it applied.
func InputsWithOverrides(root string) (harness.Inputs, []workflow.Override, error) {
	instructions, overrides, err := workflow.Load(filepath.Join(project.RalphDir(root), "workflow"))
	if err != nil {
		return harness.Inputs{}, nil, fmt.Errorf("workflow overrides: %w", err)
	}
	return harness.Inputs{
		Instructions: instructions,
		MCP:          harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
		Project:      os.DirFS(root),
	}, overrides, nil
}

// Check reports the recorded artifacts of adapters that a newer ralph would
// write differently and that the user has left as ralph wrote them, sorted by
// path. Adapters with no recorded entries are not asked for artifacts.
func Check(ctx context.Context, root string, adapters []harness.Adapter) ([]Outdated, error) {
	dir := project.RalphDir(root)
	manifests := map[harness.Scope]Manifest{}
	for scope, file := range map[harness.Scope]string{
		harness.ScopeProject: project.IntegrationFile,
		harness.ScopeLocal:   project.IntegrationLocalFile,
	} {
		m, err := LoadManifest(filepath.Join(dir, file))
		if err != nil {
			return nil, err
		}
		manifests[scope] = m
	}
	var customized []string
	for _, m := range manifests {
		customized = append(customized, m.Customized...)
	}

	var in *harness.Inputs
	var out []Outdated
	for _, ad := range adapters {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !hasEntries(manifests, ad.Name()) {
			continue
		}
		if in == nil {
			v, err := InputsFor(root)
			if err != nil {
				return nil, err
			}
			in = &v
		}
		arts, err := ad.Artifacts(*in)
		if err != nil {
			return nil, fmt.Errorf("integration: %s: %w", ad.Name(), err)
		}
		for _, a := range arts {
			if err := a.Validate(); err != nil {
				return nil, err
			}
			id := a.ID()
			if slices.Contains(customized, a.Path) || slices.Contains(customized, id) {
				continue
			}
			entry, ok := manifests[a.Scope].find(id)
			if !ok {
				continue
			}
			stale, err := isStale(root, a, entry)
			if err != nil {
				return nil, err
			}
			if stale {
				out = append(out, Outdated{ID: id, Path: a.Path, Label: a.Label})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func hasEntries(manifests map[harness.Scope]Manifest, adapter string) bool {
	for _, m := range manifests {
		for _, e := range m.Entries {
			if e.Adapter == adapter {
				return true
			}
		}
	}
	return false
}

func isStale(root string, a harness.Artifact, entry Entry) (bool, error) {
	target := filepath.Join(root, filepath.FromSlash(a.Path))
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("integration: stat %s: %w", a.Path, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return false, fmt.Errorf("integration: read %s: %w", a.Path, err)
	}
	st := inspect(a, data, true)
	if st.problem != "" || !st.present {
		return false, nil
	}
	return st.current == entry.SHA256 && st.desired != entry.SHA256, nil
}
