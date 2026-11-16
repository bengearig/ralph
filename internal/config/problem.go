package config

import (
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
