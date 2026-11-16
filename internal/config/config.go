// Package config loads ralph's layered configuration.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// Warning reports a user-only setting ignored in the project file.
type Warning struct {
	File string
	Line int
	Path string
}

func (w Warning) String() string {
	return fmt.Sprintf("%s:%d: %q is user-only and was ignored in the project file", w.File, w.Line, w.Path)
}

// Config is the merged configuration.
type Config struct {
	values   map[string]Value
	warnings []Warning
	registry *Registry
	getenv   func(string) (string, bool)
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
// flags. It reports every problem together in a *Error, ordered by layer and
// then by line.
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
	var warnings []Warning
	var problems []Problem
	for _, s := range reg.Settings() {
		if s.Default == "" {
			continue
		}
		if msg := valueProblem(s, s.Default); msg != "" {
			problems = append(problems, Problem{Source: sourceDefault, Message: msg})
			continue
		}
		values[s.Path] = Value{Path: s.Path, Value: s.Default, Layer: LayerDefault}
	}

	type layerFile struct {
		layer Layer
		path  string
	}
	files := []layerFile{{LayerUser, opts.UserFile}}
	if opts.ProjectRoot != "" {
		dir := project.RalphDir(opts.ProjectRoot)
		files = append(files,
			layerFile{LayerProject, filepath.Join(dir, project.ProjectFile)},
			layerFile{LayerProjectLocal, filepath.Join(dir, project.LocalFile)},
		)
	}
	for _, f := range files {
		if f.path == "" {
			continue
		}
		found, fileProblems := loadFile(reg, f.path, f.layer)
		for _, v := range found {
			s := reg.settings[v.Path]
			if msg := valueProblem(s, v.Value); msg != "" {
				fileProblems = append(fileProblems, Problem{File: v.Source.File, Line: v.Source.Line, Message: msg})
				continue
			}
			if f.layer == LayerProject && s.Scope == ScopeUser {
				warnings = append(warnings, Warning{File: v.Source.File, Line: v.Source.Line, Path: v.Path})
				continue
			}
			values[v.Path] = v
		}
		sort.SliceStable(fileProblems, func(i, j int) bool { return fileProblems[i].Line < fileProblems[j].Line })
		problems = append(problems, fileProblems...)
	}

	for _, s := range reg.Settings() {
		name := EnvName(s.Path)
		if v, ok := getenv(name); ok && v != "" {
			if msg := valueProblem(s, v); msg != "" {
				problems = append(problems, Problem{Source: name, Message: msg})
				continue
			}
			if s.Kind == KindList {
				items, _ := parseList(v)
				v = encodeList(items)
			}
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
			problems = append(problems, Problem{Source: "--" + name, Message: "unknown flag"})
			continue
		}
		if msg := valueProblem(s, opts.Flags[name]); msg != "" {
			problems = append(problems, Problem{Source: "--" + name, Message: msg})
			continue
		}
		values[s.Path] = Value{Path: s.Path, Value: opts.Flags[name], Layer: LayerFlag, Source: Source{Name: "--" + name}}
	}

	if len(problems) > 0 {
		return nil, &Error{Problems: problems, Warnings: warnings}
	}
	return &Config{values: values, warnings: warnings, registry: reg, getenv: getenv}, nil
}

// Get returns the value of the setting at path, if it is set.
func (c *Config) Get(path string) (Value, bool) {
	v, ok := c.values[path]
	return v, ok
}

// List returns a copy of the list held by the list setting at path, if it is
// set.
func (c *Config) List(path string) ([]string, bool) {
	s, ok := c.registry.Lookup(path)
	if !ok || s.Kind != KindList {
		return nil, false
	}
	v, ok := c.values[path]
	if !ok {
		return nil, false
	}
	items, err := parseList(v.Value)
	if err != nil {
		return nil, false
	}
	return items, true
}

// Warnings returns the warnings raised by Load, in file order.
func (c *Config) Warnings() []Warning {
	return c.warnings
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

var yamlLine = regexp.MustCompile(`line (\d+)`)

func loadFile(reg *Registry, file string, layer Layer) ([]Value, []Problem) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []Problem{{File: file, Message: fmt.Sprintf("cannot read file: %v", err)}}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		p := Problem{File: file, Message: "invalid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
		if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
		}
		return nil, []Problem{p}
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
		return nil, []Problem{{File: file, Line: root.Line, Message: "top level must be a mapping"}}
	}
	var out []Value
	var problems []Problem
	walk(reg, file, layer, root, "", &out, &problems)
	return out, problems
}

func walk(reg *Registry, file string, layer Layer, m *yaml.Node, prefix string, out *[]Value, problems *[]Problem) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		problem := func(format string, args ...any) {
			*problems = append(*problems, Problem{File: file, Line: key.Line, Message: fmt.Sprintf(format, args...)})
		}
		if key.Kind != yaml.ScalarNode || strings.Contains(key.Value, ".") {
			problem("unknown setting %q", path)
			continue
		}
		switch {
		case isSetting(reg, path) && reg.settings[path].Kind == KindList:
			items, ok := yamlItems(val)
			if !ok {
				problem("setting %q must be a list of text values", path)
				continue
			}
			*out = append(*out, Value{
				Path:   path,
				Value:  encodeList(items),
				Layer:  layer,
				Source: Source{File: file, Line: val.Line},
			})
		case isSetting(reg, path):
			if val.Kind != yaml.ScalarNode {
				problem("setting %q must be a scalar value", path)
				continue
			}
			*out = append(*out, Value{
				Path:   path,
				Value:  val.Value,
				Layer:  layer,
				Source: Source{File: file, Line: val.Line},
			})
		case reg.prefixes[path]:
			if val.Kind != yaml.MappingNode {
				problem("%q must be a mapping of settings", path)
				continue
			}
			walk(reg, file, layer, val, path, out, problems)
		default:
			problem("unknown setting %q", path)
		}
	}
}

// yamlItems returns the scalar texts of a sequence node.
func yamlItems(n *yaml.Node) ([]string, bool) {
	if n.Kind != yaml.SequenceNode {
		return nil, false
	}
	items := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
			return nil, false
		}
		items = append(items, item.Value)
	}
	return items, true
}

func isSetting(reg *Registry, path string) bool {
	_, ok := reg.settings[path]
	return ok
}
