package headless

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/verify"
)

// ReadOnlyTools are the ralph tools a restricted run may call, by bare name.
var ReadOnlyTools = []string{
	"status", "unit_get", "unit_list", "planning_settings",
	"doc_get", "doc_list", "doc_versions", "search",
}

// ReadOnlyGit are the git command patterns a restricted run may run.
var ReadOnlyGit = []string{
	"git status *", "git diff *", "git log *", "git show *", "git rev-parse *", "git ls-files *",
}

// protectedPaths are what a headless run may never edit; a trailing slash
// marks a directory.
var protectedPaths = []string{".git/", ".ralph/"}

// SimpleCommands splits a shell command line at &&, ||, ;, |, & and newlines,
// without regard to quoting, and drops empty parts.
func SimpleCommands(command string) []string {
	parts := strings.FieldsFunc(command, func(r rune) bool {
		return r == '&' || r == '|' || r == ';' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EscapePattern turns a literal command into a pattern that matches only it.
func EscapePattern(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '*' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Restricted is the allowlist of a restricted run: read-only ralph tools,
// read-only git, each verification command whole and in parts, and extra.
func Restricted(verifyCommands, extra []string) harness.Allowlist {
	seen := map[string]bool{}
	var commands []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			commands = append(commands, p)
		}
	}
	for _, p := range ReadOnlyGit {
		add(p)
	}
	for _, c := range verifyCommands {
		add(EscapePattern(c))
		for _, part := range SimpleCommands(c) {
			add(EscapePattern(part))
		}
	}
	for _, p := range extra {
		add(p)
	}
	return harness.Allowlist{
		Tools:     append([]string(nil), ReadOnlyTools...),
		Commands:  commands,
		Protected: append([]string(nil), protectedPaths...),
	}
}

// NewRequest builds a headless request for the project at root, restricted
// unless headless.permissions is auto.
func NewRequest(cfg *config.Config, root, prompt string, mcp harness.MCPServer) (harness.HeadlessRequest, error) {
	extra, _ := cfg.List("headless.allow")
	mode := harness.PermissionRestricted
	if v, ok := cfg.Get("headless.permissions"); ok && v.Value == string(harness.PermissionAuto) {
		mode = harness.PermissionAuto
	}
	req := harness.HeadlessRequest{
		Dir:    root,
		Prompt: prompt,
		MCP:    mcp,
		Mode:   mode,
		Allow:  Restricted(verify.FromConfig(cfg, root).Commands, extra),
	}
	if v, ok := cfg.Get("headless.budget_usd"); ok {
		f, err := strconv.ParseFloat(v.Value, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return harness.HeadlessRequest{}, fmt.Errorf("headless.budget_usd: %q is not a number of at least 0", v.Value)
		}
		req.BudgetUSD = f
	}
	if mode == harness.PermissionAuto {
		req.Allow.Commands = nil
		req.Allow.Tools = nil
	}
	if err := req.Validate(); err != nil {
		return harness.HeadlessRequest{}, err
	}
	return req, nil
}

// NewOptions reads the run limits from the configuration; the caller adds
// Stdout. The cost cap travels in the request and Execute copies it.
func NewOptions(cfg *config.Config) (Options, error) {
	v, ok := cfg.Get("headless.timeout")
	if !ok {
		return Options{}, fmt.Errorf("headless.timeout: not set")
	}
	d, err := time.ParseDuration(v.Value)
	if err != nil || d <= 0 {
		return Options{}, fmt.Errorf("headless.timeout: %q is not a duration greater than zero", v.Value)
	}
	return Options{Timeout: d}, nil
}
