package integration

import (
	"path/filepath"
	"sort"

	"github.com/bengearig/ralph/internal/project"
)

// LocalPaths returns the paths recorded in the local manifest of the project at
// root, sorted and without duplicates. A missing manifest gives none.
func LocalPaths(root string) ([]string, error) {
	m, err := LoadManifest(filepath.Join(project.RalphDir(root), project.IntegrationLocalFile))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(m.Entries))
	paths := make([]string, 0, len(m.Entries))
	for _, e := range m.Entries {
		if !seen[e.Path] {
			seen[e.Path] = true
			paths = append(paths, e.Path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}
