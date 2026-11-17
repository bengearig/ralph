// Package issuesource defines the role of an external system that holds the
// tickets work can start from, and the setting that chooses it.
package issuesource

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/config"
)

// Ref names one ticket in one system.
type Ref struct {
	System string
	Key    string
}

// String returns <system>:<key>.
func (r Ref) String() string { return r.System + ":" + r.Key }

// Issue is a ticket as the system holds it.
type Issue struct {
	Ref   Ref
	URL   string
	Title string
	Body  string // Markdown
	Type  string
	// Status is the system's own name for the ticket's status.
	Status string
	// Parent is the key of the parent ticket, empty when none.
	Parent string
}

// Source is a system tickets are read from.
type Source interface {
	Name() string
	// Parse turns what a person typed, a key or an address, into a Ref. An
	// error is ErrInvalid and says what is accepted.
	Parse(ref string) (Ref, error)
	// Get returns the ticket. An unknown ticket is ErrNotFound.
	Get(ctx context.Context, ref Ref) (Issue, error)
}

// Instruction is the text work on i starts from: a heading line and, when the
// body is not blank, a blank line and the body.
func Instruction(i Issue) string {
	var b strings.Builder
	b.WriteString(titleCase(i.Ref.System))
	b.WriteByte(' ')
	b.WriteString(i.Ref.Key)
	b.WriteString(": ")
	b.WriteString(i.Title)
	if strings.TrimSpace(i.Body) != "" {
		b.WriteString("\n\n")
		b.WriteString(i.Body)
	}
	return b.String()
}

func titleCase(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// Settings are the issue source settings, read without any network access.
type Settings struct {
	Provider string
}

// FromConfig reads the issue source settings from cfg.
func FromConfig(cfg *config.Config) (Settings, error) {
	v, _ := cfg.Get("issues.provider")
	return Settings{Provider: v.Value}, nil
}
