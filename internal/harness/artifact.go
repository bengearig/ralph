package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Scope says whether an artifact is committed or personal.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeLocal   Scope = "local"
)

// Kind says how an artifact is embedded in its file.
type Kind string

const (
	KindFile          Kind = "file"
	KindSection       Kind = "section"
	KindJSONEntry     Kind = "json-entry"
	KindJSONArrayItem Kind = "json-array-item"
)

const sectionMarkerPrefix = "<!-- ralph:"

// Artifact is one thing that should exist in a project.
type Artifact struct {
	Kind  Kind
	Scope Scope
	// Path is project-relative with forward slashes.
	Path string
	// KeyPath locates the value in a JSON file; used by the JSON kinds only.
	KeyPath []string
	// Content is the full text of a file, the body of a section, or the JSON
	// encoding of the value for the JSON kinds.
	Content []byte
	// Label is a short human description for output.
	Label string
}

// ID is stable and unique per artifact.
func (a Artifact) ID() string {
	switch a.Kind {
	case KindSection:
		return "section:" + a.Path
	case KindJSONEntry:
		return "json:" + a.Path + "#" + strings.Join(a.KeyPath, ".")
	case KindJSONArrayItem:
		item := string(a.Content)
		var buf bytes.Buffer
		if err := json.Compact(&buf, a.Content); err == nil {
			item = buf.String()
		}
		return "jsonitem:" + a.Path + "#" + strings.Join(a.KeyPath, ".") + "=" + item
	default:
		return "file:" + a.Path
	}
}

// Validate reports why the artifact is malformed, if it is.
func (a Artifact) Validate() error {
	if a.Path == "" {
		return fmt.Errorf("artifact: empty path")
	}
	if strings.Contains(a.Path, `\`) {
		return fmt.Errorf("artifact %s: path contains a backslash", a.Path)
	}
	if path.IsAbs(a.Path) {
		return fmt.Errorf("artifact %s: path is not relative", a.Path)
	}
	for _, seg := range strings.Split(a.Path, "/") {
		if seg == ".." {
			return fmt.Errorf("artifact %s: path contains a .. segment", a.Path)
		}
	}
	switch a.Scope {
	case ScopeProject, ScopeLocal:
	default:
		return fmt.Errorf("artifact %s: unknown scope %q", a.Path, a.Scope)
	}
	switch a.Kind {
	case KindFile, KindSection, KindJSONEntry, KindJSONArrayItem:
	default:
		return fmt.Errorf("artifact %s: unknown kind %q", a.Path, a.Kind)
	}
	if len(a.Content) == 0 {
		return fmt.Errorf("artifact %s: empty content", a.Path)
	}
	switch a.Kind {
	case KindSection:
		if bytes.Contains(a.Content, []byte(sectionMarkerPrefix)) {
			return fmt.Errorf("artifact %s: section content contains a %q marker", a.Path, sectionMarkerPrefix)
		}
	case KindJSONEntry, KindJSONArrayItem:
		if len(a.KeyPath) == 0 {
			return fmt.Errorf("artifact %s: %s needs a key path", a.Path, a.Kind)
		}
		for _, k := range a.KeyPath {
			if k == "" {
				return fmt.Errorf("artifact %s: key path has an empty element", a.Path)
			}
		}
		if !json.Valid(a.Content) {
			return fmt.Errorf("artifact %s: content is not valid JSON", a.Path)
		}
	}
	if (a.Kind == KindFile || a.Kind == KindSection) && len(a.KeyPath) != 0 {
		return fmt.Errorf("artifact %s: %s must not have a key path", a.Path, a.Kind)
	}
	return nil
}
