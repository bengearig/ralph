package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const sourceDefault = "default"

// Problem is one thing wrong with the configuration. A file problem sets File
// and Line; otherwise Source is the environment variable name, "--flag" for a
// flag, or "default".
type Problem struct {
	File    string
	Line    int
	Source  string
	Message string
}

func (p Problem) String() string {
	switch {
	case p.File != "" && p.Line > 0:
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message)
	case p.File != "":
		return fmt.Sprintf("%s: %s", p.File, p.Message)
	case p.Source == sourceDefault:
		return "default value: " + p.Message
	case strings.HasPrefix(p.Source, "--"):
		return fmt.Sprintf("flag %s: %s", p.Source, p.Message)
	default:
		return fmt.Sprintf("environment variable %s: %s", p.Source, p.Message)
	}
}

// Error carries every problem found by Load and any warnings raised on the way.
type Error struct {
	Problems []Problem
	Warnings []Warning
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

// valueProblem returns the message describing why v is not acceptable for s, or
// empty if it is. The message never contains the value of a secret setting.
func valueProblem(s Setting, v string) string {
	if s.Kind == KindList {
		return listProblem(s, v)
	}
	if s.Kind == KindSecret {
		if _, err := ParseSecretRef(v); err != nil {
			return fmt.Sprintf("setting %q %s", s.Path, err)
		}
	}
	if len(s.Allowed) > 0 && !slices.Contains(s.Allowed, v) {
		return fmt.Sprintf("invalid value for %q: must be one of %s", s.Path, strings.Join(s.Allowed, ", "))
	}
	if s.Validate != nil {
		if err := s.Validate(v); err != nil {
			return fmt.Sprintf("invalid value for %q: %s", s.Path, err)
		}
	}
	return ""
}

const (
	maxListItems     = 50
	maxListItemBytes = 4096
)

var errNotJSONList = errors.New("must be a JSON array of strings")

// parseList decodes a JSON array of strings; it never returns a nil slice on
// success.
func parseList(v string) ([]string, error) {
	if !strings.HasPrefix(strings.TrimLeft(v, " \t\r\n"), "[") {
		return nil, errNotJSONList
	}
	var raw []any
	dec := json.NewDecoder(strings.NewReader(v))
	if err := dec.Decode(&raw); err != nil || dec.More() {
		return nil, errNotJSONList
	}
	items := make([]string, 0, len(raw))
	for _, r := range raw {
		str, ok := r.(string)
		if !ok {
			return nil, errNotJSONList
		}
		items = append(items, str)
	}
	return items, nil
}

func encodeList(items []string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if items == nil {
		items = []string{}
	}
	_ = enc.Encode(items)
	return strings.TrimSuffix(buf.String(), "\n")
}

func listProblem(s Setting, v string) string {
	items, err := parseList(v)
	if err != nil {
		return err.Error()
	}
	if len(items) > maxListItems {
		return fmt.Sprintf("invalid value for %q: must have at most %d items", s.Path, maxListItems)
	}
	for i, item := range items {
		var msg string
		switch {
		case strings.Trim(item, " \t") == "":
			msg = "must not be blank"
			if s.ValidateItem != nil {
				if err := s.ValidateItem(item); err != nil {
					msg = err.Error()
				}
			}
		case len(item) > maxListItemBytes:
			msg = fmt.Sprintf("must be at most %d bytes", maxListItemBytes)
		case strings.ContainsRune(item, 0):
			msg = "must not contain a NUL byte"
		case s.ValidateItem != nil:
			if err := s.ValidateItem(item); err != nil {
				msg = err.Error()
			}
		}
		if msg != "" {
			return fmt.Sprintf("invalid value for %q: item %d: %s", s.Path, i+1, msg)
		}
	}
	return ""
}
