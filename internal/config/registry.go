package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/naming"
)

// harnessNames are the adapter names loop.harness accepts. The registry cannot
// import the adapters, which would make a cycle; TestLoopHarnessNamesMatchAdapters keeps it in step.
var harnessNames = []string{"claude-code", "opencode"}

// followUpAutoKinds are the entries follow_up.auto accepts.
var followUpAutoKinds = []string{"close", "feedback", "continue"}

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
			Path:        "documents.versions.keep",
			Description: "How many earlier versions of each document to keep; 0 keeps every earlier version and a positive number keeps that many of the most recent",
			Default:     "0",
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || strconv.Itoa(n) != v {
					return errors.New("must be a whole number of 0 or more")
				}
				return nil
			},
		},
		Setting{
			Path:        "documents.export.dir",
			Description: "Directory, relative to the project root, that receives one generated markdown file per active document; files in it that ralph generated are overwritten and removed when stale",
			Default:     "docs/ralph",
			Validate:    validateExportDir,
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
			Path:        "bitbucket.workspace",
			Description: "Bitbucket Cloud workspace; empty means detect it from the remote",
			Validate:    validateGitHubName,
		},
		Setting{
			Path:        "bitbucket.repo",
			Description: "Bitbucket Cloud repository slug; empty means detect it from the remote",
			Validate:    validateGitHubName,
		},
		Setting{
			Path:        "bitbucket.api_url",
			Description: "Base URL of the Bitbucket Cloud API; https, or http on a loopback host",
			Default:     "https://api.bitbucket.org/2.0",
			Scope:       ScopeUser,
			Validate:    validateAPIURL,
		},
		Setting{
			Path:        "bitbucket.token",
			Description: "Reference to the Bitbucket token, of the form env:NAME",
			Default:     "env:BITBUCKET_TOKEN",
			Scope:       ScopeUser,
			Kind:        KindSecret,
		},
		Setting{
			Path:        "bitbucket.user",
			Description: "Atlassian account email; when set the token is an API token sent with Basic authentication, otherwise it is sent as a Bearer token",
			Scope:       ScopeUser,
			Validate: func(v string) error {
				if strings.ContainsFunc(v, unicode.IsSpace) {
					return errors.New("must not contain whitespace")
				}
				return nil
			},
		},
		Setting{
			Path:        "pr.provider",
			Description: "Which pull request provider to use; auto picks it from the remote's host",
			Default:     "auto",
			Allowed:     []string{"auto", "github", "bitbucket"},
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
			Path:        "follow_up.delete_branches",
			Description: "Which branches ralph deletes after a pull request is merged: local deletes only the local branch, both also deletes the remote one, none deletes nothing; a branch is deleted only when its tip is the commit the merged pull request had",
			Default:     "local",
			Allowed:     []string{"local", "both", "none"},
		},
		Setting{
			Path:        "follow_up.auto",
			Description: "The kinds of follow-up that ralph apply with no flags, and ralph monitor --watch --apply, apply by themselves: close, feedback and continue; each kind is applied with the same checks as when a flag names it, and continue starts the loop for the next slice of a brief after a merge, which runs a coding agent and spends money",
			Kind:        KindList,
			Scope:       ScopeUser,
			ValidateItem: func(v string) error {
				if !slices.Contains(followUpAutoKinds, v) {
					return fmt.Errorf("must be one of %s", strings.Join(followUpAutoKinds, ", "))
				}
				return nil
			},
		},
		Setting{
			Path:        "plan.confirm_outline",
			Description: "Whether a new outline is shown and approved before anything is created; a revision of an existing outline always asks, whatever this says",
			Default:     "false",
			Allowed:     []string{"true", "false"},
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
			Path:        "taskmanager.provider",
			Description: "Which task manager ralph pushes briefs, slices and tasks to as tickets; none means no tickets are made",
			Default:     "none",
			Allowed:     []string{"none", "jira"},
		},
		Setting{
			Path:        "issues.provider",
			Description: "Which system ralph reads tickets from when work starts from one; none turns reading off, and jira turns it on, fetching the ticket's text from the site named by jira.site",
			Default:     "none",
			Allowed:     []string{"none", "jira"},
		},
		Setting{
			Path:        "jira.site",
			Description: "Address of the Jira Cloud site, https or http on a loopback host, with no credentials, query or fragment; ticket text is sent to this site when taskmanager.provider is jira",
			Scope:       ScopeUser,
			Validate:    validateJiraSite,
		},
		Setting{
			Path:        "jira.user",
			Description: "Email of the Atlassian account that owns the API token; ticket text is sent to the site named by jira.site on its behalf",
			Scope:       ScopeUser,
			Validate:    validateJiraUser,
		},
		Setting{
			Path:        "jira.token",
			Description: "Reference to the Jira API token, of the form env:NAME; ticket text is sent to the site named by jira.site with it",
			Default:     "env:JIRA_TOKEN",
			Scope:       ScopeUser,
			Kind:        KindSecret,
		},
		Setting{
			Path:        "jira.project",
			Description: "Key of the Jira project that receives the tickets, 2 to 20 characters: an uppercase letter, then uppercase letters, digits and underscores",
			Validate:    validateJiraProject,
		},
		Setting{
			Path:        "jira.type.brief",
			Description: "Ticket type made for a brief; empty means briefs have no ticket",
			Default:     "Feature",
			Validate:    validateJiraName,
		},
		Setting{
			Path:        "jira.type.slice",
			Description: "Ticket type made for a slice; empty means slices have no ticket",
			Default:     "Task",
			Validate:    validateJiraName,
		},
		Setting{
			Path:        "jira.type.task",
			Description: "Ticket type made for a task; empty means tasks have no ticket",
			Validate:    validateJiraName,
		},
		Setting{
			Path:         "jira.states",
			Description:  "Items STATE=Status that name the ticket status a ralph state maps to, replacing the built-in mapping; STATE is PROPOSED, TODO, IN_PROGRESS, BLOCKED, DONE, CANCELLED, EMPTY, ARTICULATING, READY, NEEDS_ATTENTION, IN_REVIEW or MERGED, and the later of two items for one state wins",
			Kind:         KindList,
			ValidateItem: validateJiraStateItem,
		},
		Setting{
			Path:         "jira.flow",
			Description:  "Status names in the order a ticket normally moves; a ticket with no direct transition to a status is stepped through the statuses between them",
			Kind:         KindList,
			ValidateItem: validateJiraStatus,
		},
		Setting{
			Path:        "jira.sprint",
			Description: "Name of the sprint new tickets are put in, at most 100 characters; empty means no sprint",
			Validate: func(v string) error {
				if utf8.RuneCountInString(v) > 100 {
					return errors.New("must be at most 100 characters")
				}
				if strings.ContainsFunc(v, unicode.IsControl) {
					return errors.New("must not contain a control character")
				}
				return nil
			},
		},
		Setting{
			Path:        "jira.board",
			Description: "Id of the board that holds the sprint, only needed when the project has several boards; empty means the project's board",
			Validate: func(v string) error {
				if v == "" {
					return nil
				}
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || strconv.Itoa(n) != v {
					return errors.New("must be a whole number of at least 1")
				}
				return nil
			},
		},
		Setting{
			Path:        "jira.sprint_levels",
			Description: "The levels brief, slice and task whose tickets are put in jira.sprint; empty means slice and task",
			Kind:        KindList,
			ValidateItem: func(v string) error {
				if !slices.Contains(jiraLevels, v) {
					return fmt.Errorf("must be one of %s", strings.Join(jiraLevels, ", "))
				}
				return nil
			},
		},
		Setting{
			Path:        "jira.auto_sync",
			Description: "Whether tickets are pushed automatically after a command or tool call that wrote something; false leaves it to ralph tickets sync",
			Default:     "true",
			Allowed:     []string{"true", "false"},
		},
		Setting{
			Path:        "jira.poll_interval",
			Description: "The least time between automatic reads of the tickets for edits made in Jira, from 1m to 24h",
			Default:     "5m",
			Validate:    durationBetween(time.Minute, 24*time.Hour, "5m"),
		},
		Setting{
			Path:        "jira.sync_timeout",
			Description: "The longest an automatic push to Jira may take, from 1s to 5m",
			Default:     "15s",
			Validate:    durationBetween(time.Second, 5*time.Minute, "15s"),
		},
		Setting{
			Path:        "jira.timeout",
			Description: "Per-request timeout for Jira, from 1s to 5m",
			Default:     "30s",
			Validate:    durationBetween(time.Second, 5*time.Minute, "30s"),
		},
		Setting{
			Path:        "embeddings.provider",
			Description: "Which embedding service semantic search uses: none means keyword search only, ollama sends document text to the service named by embeddings.url",
			Default:     "none",
			Scope:       ScopeUser,
			Allowed:     []string{"none", "ollama"},
		},
		Setting{
			Path:        "embeddings.url",
			Description: "Address of the embedding service, http or https with no credentials, query or fragment; document text is sent to this address when embeddings.provider is not none",
			Default:     "http://localhost:11434",
			Scope:       ScopeUser,
			Validate:    validateEmbeddingsURL,
		},
		Setting{
			Path:        "embeddings.model",
			Description: "The embedding model the service at embeddings.url runs; document text is embedded with it",
			Default:     "nomic-embed-text",
			Scope:       ScopeUser,
			Validate:    validateEmbeddingsModel,
		},
		Setting{
			Path:        "embeddings.document_prefix",
			Description: "Text put before each document chunk sent to the service at embeddings.url; empty means the built-in prefix for the model, and none means no prefix",
			Scope:       ScopeUser,
			Validate:    validateEmbeddingsPrefix,
		},
		Setting{
			Path:        "embeddings.query_prefix",
			Description: "Text put before each search query sent to the service at embeddings.url; empty means the built-in prefix for the model, and none means no prefix",
			Scope:       ScopeUser,
			Validate:    validateEmbeddingsPrefix,
		},
		Setting{
			Path:        "embeddings.chunk_chars",
			Description: "Target size in characters of each piece of document text sent to the service at embeddings.url, from 200 to 6000",
			Default:     "1200",
			Scope:       ScopeUser,
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return errors.New("must be a whole number from 200 to 6000")
				}
				if n < 200 || n > 6000 {
					return errors.New("must be between 200 and 6000")
				}
				return nil
			},
		},
		Setting{
			Path:        "embeddings.timeout",
			Description: "Per-request timeout for the embedding service at embeddings.url, greater than zero and at most 10m",
			Default:     "30s",
			Scope:       ScopeUser,
			Validate: func(v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return errors.New("must be a duration such as 30s")
				}
				if d <= 0 || d > 10*time.Minute {
					return errors.New("must be greater than zero and at most 10m")
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
			Path:        "headless.allow",
			Description: "Shell command patterns the headless agent may run in restricted mode; * matches any text, a pattern ending in ` *` also matches the command with no arguments, and a backslash takes the next character literally",
			Kind:        KindList,
			Scope:       ScopeUser,
			ValidateItem: func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("must not be blank")
				}
				if strings.ContainsFunc(v, unicode.IsControl) {
					return errors.New("must not contain a newline or other control character")
				}
				if utf8.RuneCountInString(v) > 500 {
					return errors.New("must be at most 500 characters")
				}
				return nil
			},
		},
		Setting{
			Path:        "headless.permissions",
			Description: "How the headless agent is permitted to act: restricted allows only a short allowlist; auto hands approval to the harness's own automatic mode, which then decides what the agent may do, and only ralph's protected paths stay denied",
			Default:     "restricted",
			Allowed:     []string{"restricted", "auto"},
			Scope:       ScopeUser,
		},
		Setting{
			Path:        "headless.budget_usd",
			Description: "Caps the cost of one headless run in US dollars; 0 means no cap. For OpenCode the step that crosses the cap has already been paid for, so the recorded cost can exceed it, and a harness that reports no cost cannot be checked",
			Default:     "0",
			Validate: func(v string) error {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
					return errors.New("must be a number of at least 0, such as 5 or 0.25")
				}
				return nil
			},
		},
		Setting{
			Path:        "headless.timeout",
			Description: "How long one headless run may take, a duration greater than zero",
			Default:     "30m",
			Validate: func(v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return errors.New("must be a duration such as 30m")
				}
				if d <= 0 {
					return errors.New("must be greater than zero")
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
			Path:        "loop.prompt_documents_bytes",
			Description: "Limits the bytes of linked-document text in a loop prompt; 0 means no limit. Documents are taken task first, then slice, then brief, and a document that does not fit is replaced by a pointer to read it with doc_get",
			Default:     "0",
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return errors.New("must be a whole number of at least 0, such as 60000")
				}
				return nil
			},
		},
		Setting{
			Path:        "loop.max_failures",
			Description: "The number of consecutive failed attempts on one task after which the loop stops and blocks the task",
			Default:     "3",
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					return errors.New("must be a whole number of at least 1, such as 3")
				}
				return nil
			},
		},
		Setting{
			Path:        "runs.keep_days",
			Description: "Run records older than this many days are removed when a loop starts; 0 keeps them for ever",
			Default:     "30",
			Validate: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return errors.New("must be a whole number of at least 0, such as 30")
				}
				return nil
			},
		},
		Setting{
			Path:        "loop.harness",
			Description: "The harness the loop runs, by adapter name; empty means the one installed harness, and several installed is an error",
			Validate: func(v string) error {
				if v == "" || slices.Contains(harnessNames, v) {
					return nil
				}
				return errors.New("must be one of " + strings.Join(harnessNames, ", ") + ", or empty")
			},
		},
		Setting{
			Path:        "loop.model",
			Description: "The model the loop asks the harness for; empty means the harness default",
			Validate: func(v string) error {
				if strings.HasPrefix(v, "-") {
					return errors.New(`must not start with "-"`)
				}
				for _, c := range v {
					if unicode.IsSpace(c) || unicode.IsControl(c) {
						return errors.New("must not contain a space or control character")
					}
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
		return errors.New("must be a URL such as https://api.example.com")
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

func validateExportDir(v string) error {
	switch {
	case v == "":
		return errors.New("must not be empty")
	case strings.ContainsRune(v, '\\'):
		return errors.New("must use forward slashes")
	case strings.HasPrefix(v, "/") || (len(v) >= 2 && v[1] == ':'):
		return errors.New("must be relative to the project root")
	}
	clean := path.Clean(v)
	if clean == "." {
		return errors.New("must be a directory below the project root, not the root itself")
	}
	elems := strings.Split(clean, "/")
	for _, e := range elems {
		if e == ".." {
			return errors.New("must stay inside the project: no \"..\" element")
		}
	}
	if elems[0] == ".git" || elems[0] == ".ralph" {
		return fmt.Errorf("must not be inside %s", elems[0])
	}
	return nil
}

var jiraLevels = []string{"brief", "slice", "task"}

var jiraStates = []string{
	"PROPOSED", "TODO", "IN_PROGRESS", "BLOCKED", "DONE", "CANCELLED",
	"EMPTY", "ARTICULATING", "READY", "NEEDS_ATTENTION", "IN_REVIEW", "MERGED",
}

var jiraProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func durationBetween(lo, hi time.Duration, example string) func(string) error {
	return func(v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return errors.New("must be a duration such as " + example)
		}
		if d < lo || d > hi {
			return fmt.Errorf("must be between %s and %s", lo, hi)
		}
		return nil
	}
}

func validateJiraSite(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Hostname() == "" {
		return errors.New("must be a URL such as https://example.atlassian.net")
	}
	if u.User != nil {
		return errors.New("jira.site must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(v, "#") {
		return errors.New("must not contain a query or fragment")
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

func validateJiraUser(v string) error {
	switch {
	case v == "":
		return nil
	case strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return errors.New("must not contain whitespace or a control character")
	case utf8.RuneCountInString(v) > 254:
		return errors.New("must be at most 254 characters")
	case !strings.Contains(v, "@"):
		return errors.New("must be an email address")
	}
	return nil
}

func validateJiraProject(v string) error {
	if v == "" {
		return nil
	}
	if n := utf8.RuneCountInString(v); n < 2 || n > 20 || !jiraProjectKey.MatchString(v) {
		return errors.New("must be 2 to 20 characters, start with an uppercase letter and contain only uppercase letters, digits and underscores")
	}
	return nil
}

// validateJiraName accepts an empty value or a ticket type name.
func validateJiraName(v string) error {
	switch {
	case utf8.RuneCountInString(v) > 60:
		return errors.New("must be at most 60 characters")
	case strings.ContainsFunc(v, unicode.IsControl):
		return errors.New("must not contain a control character")
	case v != strings.TrimSpace(v):
		return errors.New("must not start or end with a space")
	}
	return nil
}

func validateJiraStatus(v string) error {
	if n := utf8.RuneCountInString(v); n < 1 || n > 60 {
		return errors.New("must be 1 to 60 characters")
	}
	if strings.ContainsFunc(v, unicode.IsControl) {
		return errors.New("must not contain a control character")
	}
	return nil
}

func validateJiraStateItem(v string) error {
	state, status, ok := strings.Cut(v, "=")
	if !ok {
		return errors.New("must be of the form STATE=Status")
	}
	if !slices.Contains(jiraStates, state) {
		return fmt.Errorf("state must be one of %s", strings.Join(jiraStates, ", "))
	}
	return validateJiraStatus(status)
}

func validateEmbeddingsURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Hostname() == "" {
		return errors.New("must be a URL such as http://localhost:11434")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if u.User != nil {
		return errors.New("embeddings.url must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(v, "#") {
		return errors.New("must not contain a query or fragment")
	}
	return nil
}

func validateEmbeddingsModel(v string) error {
	switch {
	case v == "":
		return errors.New("must not be empty")
	case utf8.RuneCountInString(v) > 200:
		return errors.New("must be at most 200 characters")
	case strings.HasPrefix(v, "-"):
		return errors.New("must not start with -")
	case strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return errors.New("must not contain whitespace or a control character")
	}
	return nil
}

func validateEmbeddingsPrefix(v string) error {
	if utf8.RuneCountInString(v) > 200 {
		return errors.New("must be at most 200 characters")
	}
	if strings.ContainsAny(v, "\r\n") {
		return errors.New("must not contain a newline")
	}
	return nil
}
