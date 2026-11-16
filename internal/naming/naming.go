// Package naming builds branch names and commit messages from templates.
package naming

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Data is what a template can refer to.
type Data struct {
	ID       string
	Level    string
	Title    string
	Slug     string
	ParentID string
	Number   int
}

const maxSlug = 40

// Slugify reduces a title to lowercase ASCII letters, digits and single hyphens.
func Slugify(title string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
		default:
			pendingHyphen = true
			continue
		}
		if pendingHyphen && b.Len() > 0 {
			b.WriteByte('-')
		}
		pendingHyphen = false
		b.WriteRune(r)
	}
	s := b.String()
	if len(s) > maxSlug {
		if s[maxSlug] == '-' {
			s = s[:maxSlug]
		} else if i := strings.LastIndexByte(s[:maxSlug], '-'); i > 0 {
			s = s[:i]
		} else {
			s = s[:maxSlug]
		}
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return "work"
	}
	return s
}

func render(tmpl string, d Data) (string, error) {
	t, err := template.New("naming").Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"trunc": func(n int, s string) string {
			r := []rune(s)
			if n < 0 || len(r) <= n {
				return s
			}
			return string(r[:n])
		},
	}).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}

var sampleData = Data{ID: "S-1", Level: "slice", Number: 1, Title: "Sample title", Slug: "sample-title", ParentID: "B-1"}

// ValidateBranchTemplate checks that tmpl parses and renders a valid branch name.
func ValidateBranchTemplate(tmpl string) error {
	_, err := RenderBranch(tmpl, sampleData)
	return err
}

// ValidateSubjectTemplate checks that tmpl parses and renders.
func ValidateSubjectTemplate(tmpl string) error {
	_, err := RenderSubject(tmpl, sampleData)
	return err
}
