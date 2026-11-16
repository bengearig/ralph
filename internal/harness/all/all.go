// Package all lists every adapter ralph ships.
package all

import (
	"sort"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/harness/opencode"
)

// Adapters returns every adapter; add one line per adapter.
func Adapters() []harness.Adapter {
	return []harness.Adapter{
		claudecode.New(),
		opencode.New(),
	}
}

// ByName finds an adapter by its name.
func ByName(name string) (harness.Adapter, bool) {
	for _, a := range Adapters() {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// Names returns the adapter names, sorted.
func Names() []string {
	names := make([]string, 0)
	for _, a := range Adapters() {
		names = append(names, a.Name())
	}
	sort.Strings(names)
	return names
}
