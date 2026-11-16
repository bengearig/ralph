package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bengearig/ralph/internal/naming"
)

// Scope says which files may set a setting.
type Scope int

const (
	// ScopeProject settings may appear in any layer.
	ScopeProject Scope = iota
	// ScopeUser settings are ignored in the committed project file.
	ScopeUser
)

// Setting declares one configuration setting. Flag is the command-line flag
// name without dashes, empty if the setting has none.
type Setting struct {
	Path        string
	Description string
	Default     string
	Flag        string
	Scope       Scope
	Kind        Kind
	// Allowed, if non-empty, lists the only values the setting accepts.
	Allowed []string
	// Validate, if set, rejects values the setting does not accept.
	Validate func(string) error
	// ValidateItem, if set, rejects list items the setting does not accept.
	ValidateItem func(string) error
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
		if s.Kind == KindList {
			if s.Default != "" || s.Flag != "" || s.Validate != nil || len(s.Allowed) > 0 {
				return nil, fmt.Errorf("config: list setting %q must not have a default, flag, validator or allowed values", s.Path)
			}
		} else if s.ValidateItem != nil {
			return nil, fmt.Errorf("config: setting %q has an item validator but is not a list setting", s.Path)
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
			Allowed:     []string{"debug", "info", "warn", "error"},
		},
		Setting{
			Path:        "storage.migrate",
			Description: "Whether pending database migrations run automatically or wait for `ralph migrate`",
			Default:     "auto",
			Allowed:     []string{"auto", "manual"},
		},
		Setting{
			Path:        "storage.backups.keep",
			Description: "How many pre-migration database backups to keep",
			Default:     "5",
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || strconv.Itoa(n) != v {
					return errors.New("must be a whole number of at least 1")
				}
				return nil
			},
		},
		Setting{
			Path:        "github.owner",
			Description: "Owner of the GitHub repository; empty means detect it from the remote",
			Validate:    validateGitHubName,
		},
		Setting{
			Path:        "github.repo",
			Description: "Name of the GitHub repository; empty means detect it from the remote",
			Validate:    validateGitHubName,
		},
		Setting{
			Path:        "github.api_url",
			Description: "Base URL of the GitHub API, for GitHub Enterprise; https, or http on a loopback host",
			Default:     "https://api.github.com",
			Scope:       ScopeUser,
			Validate:    validateAPIURL,
		},
		Setting{
			Path:        "github.token",
			Description: "Reference to the GitHub token, of the form env:NAME",
			Default:     "env:GITHUB_TOKEN",
			Scope:       ScopeUser,
			Kind:        KindSecret,
		},
		Setting{
			Path:        "pr.provider",
			Description: "Which pull request provider to use",
			Default:     "github",
			Allowed:     []string{"github"},
		},
		Setting{
			Path:        "pr.remote",
			Description: "The git remote the branch is pushed to",
			Default:     "origin",
			Validate: func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("must not be blank")
				}
				if strings.ContainsFunc(v, unicode.IsSpace) {
					return errors.New("must not contain whitespace")
				}
				return nil
			},
		},
		Setting{
			Path:        "pr.base_branch",
			Description: "The branch pull requests target; empty means the remote's default branch, else main",
			Validate:    validateBranchName,
		},
		Setting{
			Path:        "pr.draft",
			Description: "Whether pull requests open as drafts",
			Default:     "true",
			Allowed:     []string{"true", "false"},
		},
		Setting{
			Path:        "pr.timeout",
			Description: "Per-request timeout for the pull request provider, from 1s to 5m",
			Default:     "30s",
			Validate: func(v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return errors.New("must be a duration such as 30s")
				}
				if d < time.Second || d > 5*time.Minute {
					return errors.New("must be between 1s and 5m")
				}
				return nil
			},
		},
		Setting{
			Path:        "verify.commands",
			Description: "Shell command lines run in order from the project root before quick work is committed",
			Kind:        KindList,
			ValidateItem: func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("must not be blank")
				}
				return nil
			},
		},
		Setting{
			Path:        "verify.timeout",
			Description: "How long each verification command may run, from 1s to 2h",
			Default:     "10m",
			Validate: func(v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return errors.New("must be a duration such as 10m")
				}
				if d < time.Second || d > 2*time.Hour {
					return errors.New("must be between 1s and 2h")
				}
				return nil
			},
		},
		Setting{
			Path:        "git.branch_template",
			Description: "Template for branch names; fields ID, Level, Number, Title, Slug, ParentID; functions lower, upper, trunc",
			Default:     "ralph/{{lower .ID}}-{{.Slug}}",
			Validate:    naming.ValidateBranchTemplate,
		},
		Setting{
			Path:        "git.commit_template",
			Description: "Template for commit subjects; fields ID, Level, Number, Title, Slug, ParentID; functions lower, upper, trunc",
			Default:     "{{.ID}}: {{.Title}}",
			Validate:    naming.ValidateSubjectTemplate,
		},
		Setting{
			Path:        "git.commit_trailer",
			Description: "Whether commits carry a Ralph-Task trailer naming their task",
			Default:     "true",
			Allowed:     []string{"true", "false"},
		},
		Setting{
			Path:        "project.id",
			Description: "Identifier of the ralph project",
			Validate: func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("must not be empty")
				}
				return nil
			},
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

// validateBranchName accepts an empty value or a name git allows for a branch.
func validateBranchName(v string) error {
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "/") || strings.HasSuffix(v, "/") ||
		strings.HasSuffix(v, ".") || v == "@" {
		return errors.New("is not a valid git branch name")
	}
	for _, bad := range []string{"..", "@{", "//"} {
		if strings.Contains(v, bad) {
			return fmt.Errorf("must not contain %q", bad)
		}
	}
	if strings.ContainsFunc(v, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("~^:?*[\\", r)
	}) {
		return errors.New("contains a character git does not allow in branch names")
	}
	for _, part := range strings.Split(v, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return errors.New("is not a valid git branch name")
		}
	}
	return nil
}

// validateGitHubName accepts an empty value or a name GitHub allows for an owner or repository.
func validateGitHubName(v string) error {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return errors.New("must contain only letters, digits, hyphens, underscores and dots")
		}
	}
	return nil
}

func validateAPIURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return errors.New("must be a URL such as https://api.github.com")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("must use https unless the host is a loopback address")
}
