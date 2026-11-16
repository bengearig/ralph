// Package config loads ralph's layered configuration.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/bengearig/ralph/internal/project"
)

// Layer is a place a value can come from, in increasing precedence.
type Layer int

const (
	LayerDefault Layer = iota
	LayerUser
	LayerProject
	LayerProjectLocal
	LayerEnv
	LayerFlag
)

var layerNames = [...]string{"default", "user", "project", "project-local", "env", "flag"}

func (l Layer) String() string {
	if l < 0 || int(l) >= len(layerNames) {
		return fmt.Sprintf("layer(%d)", int(l))
	}
	return layerNames[l]
}

// Source locates a value: File and Line for a file, Name for an environment
// variable or a flag.
type Source struct {
	File string
	Line int
	Name string
}

// Value is the winning value of one setting.
type Value struct {
	Path   string
	Value  string
	Layer  Layer
	Source Source
}

// Options controls Load. An empty ProjectRoot skips both project layers and an
// empty UserFile skips the user layer. Flags holds only flags actually set.
type Options struct {
	Registry    *Registry
	UserFile    string
	ProjectRoot string
	Getenv      func(string) (string, bool)
	Flags       map[string]string
}

// Config is the merged configuration.
type Config struct {
	values map[string]Value
}

// DefaultUserFile returns the user configuration file, or empty if the user
// configuration directory cannot be determined.
func DefaultUserFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "ralph", "config.yaml")
}

// Load merges defaults, the user file, the project files, the environment and
// flags. It stops at the first problem.
func Load(opts Options) (*Config, error) {
	reg := opts.Registry
	if reg == nil {
		reg = DefaultRegistry()
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.LookupEnv
	}

	values := map[string]Value{}
	for _, s := range reg.Settings() {
		if s.Default != "" {
			values[s.Path] = Value{Path: s.Path, Value: s.Default, Layer: LayerDefault}
		}
	}

	files := []struct {
		layer Layer
		path  string
	}{{LayerUser, opts.UserFile}}
	if opts.ProjectRoot != "" {
		dir := project.RalphDir(opts.ProjectRoot)
		files = append(files,
			struct {
				layer Layer
				path  string
			}{LayerProject, filepath.Join(dir, project.ProjectFile)},
			struct {
				layer Layer
				path  string
			}{LayerProjectLocal, filepath.Join(dir, project.LocalFile)},
		)
	}
	for _, f := range files {
		if f.path == "" {
			continue
		}
		found, err := loadFile(reg, f.path, f.layer)
		if err != nil {
			return nil, err
		}
		for _, v := range found {
			values[v.Path] = v
		}
	}

	for _, s := range reg.Settings() {
		name := EnvName(s.Path)
		if v, ok := getenv(name); ok && v != "" {
			values[s.Path] = Value{Path: s.Path, Value: v, Layer: LayerEnv, Source: Source{Name: name}}
		}
	}

	byFlag := map[string]Setting{}
	for _, s := range reg.Settings() {
		if s.Flag != "" {
			byFlag[s.Flag] = s
		}
	}
	names := make([]string, 0, len(opts.Flags))
	for name := range opts.Flags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s, ok := byFlag[name]
		if !ok {
			return nil, fmt.Errorf("config: unknown flag %q", name)
		}
		values[s.Path] = Value{Path: s.Path, Value: opts.Flags[name], Layer: LayerFlag, Source: Source{Name: "--" + name}}
	}

	return &Config{values: values}, nil
}

// Get returns the value of the setting at path, if it is set.
func (c *Config) Get(path string) (Value, bool) {
	v, ok := c.values[path]
	return v, ok
}

// Values returns every set value sorted by path.
func (c *Config) Values() []Value {
	out := make([]Value, 0, len(c.values))
	for _, v := range c.values {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func loadFile(reg *Registry, file string, layer Layer) ([]Value, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", file, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config: %s: invalid YAML: %w", file, err)
	}
	if doc.Kind == 0 {
		return nil, nil
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, nil
		}
		root = root.Content[0]
	}
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config: %s:%d: top level must be a mapping", file, root.Line)
	}
	var out []Value
	if err := walk(reg, file, layer, root, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(reg *Registry, file string, layer Layer, m *yaml.Node, prefix string, out *[]Value) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		if key.Kind != yaml.ScalarNode || strings.Contains(key.Value, ".") {
			return fmt.Errorf("config: %s:%d: unknown setting %q", file, key.Line, path)
		}
		switch {
		case isSetting(reg, path):
			if val.Kind != yaml.ScalarNode {
				return fmt.Errorf("config: %s:%d: setting %q must be a scalar value", file, key.Line, path)
			}
			*out = append(*out, Value{
				Path:   path,
				Value:  val.Value,
				Layer:  layer,
				Source: Source{File: file, Line: val.Line},
			})
		case reg.prefixes[path]:
			if val.Kind != yaml.MappingNode {
				return fmt.Errorf("config: %s:%d: %q must be a mapping of settings", file, key.Line, path)
			}
			if err := walk(reg, file, layer, val, path, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("config: %s:%d: unknown setting %q", file, key.Line, path)
		}
	}
	return nil
}

func isSetting(reg *Registry, path string) bool {
	_, ok := reg.settings[path]
	return ok
}
