package config

import (
	"fmt"
	"sort"
	"strings"
)

// Setting declares one configuration setting. Flag is the command-line flag
// name without dashes, empty if the setting has none.
type Setting struct {
	Path        string
	Description string
	Default     string
	Flag        string
}

// Registry is the set of settings a configuration may contain.
type Registry struct {
	settings map[string]Setting
	prefixes map[string]bool
	flags    map[string]string
}

// NewRegistry validates and collects settings.
func NewRegistry(settings ...Setting) (*Registry, error) {
	r := &Registry{
		settings: make(map[string]Setting, len(settings)),
		prefixes: map[string]bool{},
		flags:    map[string]string{},
	}
	for _, s := range settings {
		if s.Path == "" {
			return nil, fmt.Errorf("config: setting with empty path")
		}
		segments := strings.Split(s.Path, ".")
		for _, seg := range segments {
			if seg == "" {
				return nil, fmt.Errorf("config: setting path %q has an empty segment", s.Path)
			}
		}
		if _, dup := r.settings[s.Path]; dup {
			return nil, fmt.Errorf("config: duplicate setting %q", s.Path)
		}
		if s.Flag != "" {
			if other, dup := r.flags[s.Flag]; dup {
				return nil, fmt.Errorf("config: flag %q declared by both %q and %q", s.Flag, other, s.Path)
			}
			r.flags[s.Flag] = s.Path
		}
		r.settings[s.Path] = s
		for i := 1; i < len(segments); i++ {
			r.prefixes[strings.Join(segments[:i], ".")] = true
		}
	}
	for path := range r.settings {
		if r.prefixes[path] {
			return nil, fmt.Errorf("config: setting %q is also a prefix of another setting", path)
		}
	}
	return r, nil
}

// Lookup returns the setting registered at path.
func (r *Registry) Lookup(path string) (Setting, bool) {
	s, ok := r.settings[path]
	return s, ok
}

// Settings returns every setting sorted by path.
func (r *Registry) Settings() []Setting {
	out := make([]Setting, 0, len(r.settings))
	for _, s := range r.settings {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// DefaultRegistry returns the settings ralph ships with.
func DefaultRegistry() *Registry {
	r, err := NewRegistry(
		Setting{
			Path:        "log.level",
			Description: "Minimum severity of log messages written to stderr",
			Default:     "info",
			Flag:        "log-level",
		},
		Setting{
			Path:        "project.id",
			Description: "Identifier of the ralph project",
		},
	)
	if err != nil {
		panic(err)
	}
	return r
}

// EnvName returns the environment variable that sets the setting at path.
func EnvName(path string) string {
	return "RALPH_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}
