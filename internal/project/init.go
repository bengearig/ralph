package project

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// NewProjectID returns a random version 4 UUID in lowercase hyphenated form.
func NewProjectID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("project: generate id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// EnsureProjectFile makes sure <root>/.ralph/project.yaml holds a non-blank
// project.id, keeping every other key and comment. A non-blank id is never
// changed and the file is then left untouched.
func EnsureProjectFile(root string) (id string, changed bool, err error) {
	path, out, id, changed, err := planProjectFile(root)
	if err != nil || !changed {
		return id, false, err
	}
	if err := os.MkdirAll(RalphDir(root), 0o755); err != nil {
		return "", false, fmt.Errorf("project: create %s: %w", RalphDir(root), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", false, fmt.Errorf("project: write %s: %w", path, err)
	}
	return id, true, nil
}

// CheckProjectFile reports whether EnsureProjectFile could process the
// project file, writing nothing.
func CheckProjectFile(root string) error {
	_, _, _, _, err := planProjectFile(root)
	return err
}

func planProjectFile(root string) (path string, out []byte, id string, changed bool, err error) {
	path = filepath.Join(RalphDir(root), ProjectFile)
	data, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return "", nil, "", false, fmt.Errorf("project: read %s: %w", path, err)
	}

	var doc yaml.Node
	if !missing {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return "", nil, "", false, fmt.Errorf("project: %s: invalid YAML: %s", path, strings.TrimPrefix(err.Error(), "yaml: "))
		}
	}

	var top *yaml.Node
	switch {
	case doc.Kind == 0:
	case doc.Kind == yaml.DocumentNode && len(doc.Content) == 1:
		top = doc.Content[0]
	default:
		return "", nil, "", false, fmt.Errorf("project: %s: unexpected document structure", path)
	}
	if top != nil && top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		top = nil
	}
	if top != nil && top.Kind != yaml.MappingNode {
		return "", nil, "", false, fmt.Errorf("project: %s: top level must be a mapping", path)
	}

	if top == nil {
		if id, err = NewProjectID(); err != nil {
			return "", nil, "", false, err
		}
		out = append(out, data...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, "project:\n  id: "+id+"\n"...)
	} else {
		sect := mapValue(top, "project")
		switch {
		case sect == nil:
			sect = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "project"}, sect)
		case sect.Kind == yaml.ScalarNode && sect.Tag == "!!null":
			*sect = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: sect.HeadComment, LineComment: sect.LineComment}
		case sect.Kind != yaml.MappingNode:
			return "", nil, "", false, fmt.Errorf("project: %s: project must be a mapping", path)
		}
		val := mapValue(sect, "id")
		if val != nil && val.Kind != yaml.ScalarNode {
			return "", nil, "", false, fmt.Errorf("project: %s: project.id must be a string", path)
		}
		if val != nil && strings.TrimSpace(val.Value) != "" {
			return path, nil, val.Value, false, nil
		}
		if id, err = NewProjectID(); err != nil {
			return "", nil, "", false, err
		}
		if val == nil {
			sect.Content = append(sect.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "id"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id})
		} else {
			*val = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id, HeadComment: val.HeadComment, LineComment: val.LineComment, FootComment: val.FootComment}
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return "", nil, "", false, fmt.Errorf("project: encode %s: %w", path, err)
		}
		if err := enc.Close(); err != nil {
			return "", nil, "", false, fmt.Errorf("project: encode %s: %w", path, err)
		}
		out = buf.Bytes()
	}

	return path, out, id, true, nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
