//go:build !windows

package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/harness/opencode"
)

const e2ePrompt = "UNIQUE-PROMPT-7731 implement the task"

type e2eHarness struct {
	name    string
	adapter harness.Adapter
}

var e2eHarnesses = []e2eHarness{
	{"claude", claudecode.New()},
	{"opencode", opencode.New()},
}

func forEachHarness(t *testing.T, f func(t *testing.T, h e2eHarness)) {
	t.Helper()
	for _, h := range e2eHarnesses {
		t.Run(h.name, func(t *testing.T) { f(t, h) })
	}
}

type e2eProject struct {
	root string
	cfg  *config.Config
}

// newE2EProject is a git repository whose .ralph/project.yaml is projectYAML,
// with settings loaded through the configuration loader.
func newE2EProject(t *testing.T, projectYAML string, env map[string]string) e2eProject {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if projectYAML != "" {
		dir := filepath.Join(root, ".ralph")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e2eProject{root: root, cfg: cfg}
}

// fakeScript describes what a fake harness prints and does.
type fakeScript struct {
	fixture string
	exit    int
	// after is shell run once the fixture is printed; {rec} stands for the
	// record directory.
	after string
}

type fakeRecord struct{ dir string }

func (r fakeRecord) path(name string) string { return filepath.Join(r.dir, name) }

func (r fakeRecord) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(r.path(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r fakeRecord) args(t *testing.T) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(r.read(t, "args"), "\x00"), "\x00")
}

func (r fakeRecord) envValue(t *testing.T, key string) (string, bool) {
	t.Helper()
	for _, line := range strings.Split(r.read(t, "env"), "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// installFake puts a fake harness first on the path. It records its arguments,
// environment and standard input in the returned directory.
func installFake(t *testing.T, h e2eHarness, s fakeScript) fakeRecord {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "e2e", h.name, s.fixture+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Fatal(err)
	}
	rec := fakeRecord{dir: t.TempDir()}
	bin := t.TempDir()
	body := fmt.Sprintf(`#!/bin/sh
printf '%%s\0' "$@" > '%[1]s/args'
env > '%[1]s/env'
cat > '%[1]s/stdin'
cat '%[2]s'
%[3]s
exit %[4]d
`, rec.dir, fixture, strings.ReplaceAll(s.after, "{rec}", rec.dir), s.exit)
	if err := os.WriteFile(filepath.Join(bin, h.name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec
}

func (p e2eProject) execute(t *testing.T, h e2eHarness) (Result, harness.HeadlessRequest) {
	t.Helper()
	mcp := harness.MCPServer{Name: "ralph", Command: "/opt/ralph/bin/ralph", Args: []string{"mcp"}}
	req, err := NewRequest(p.cfg, p.root, e2ePrompt, mcp)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := NewOptions(p.cfg)
	if err != nil {
		t.Fatal(err)
	}
	opt.Grace = time.Second
	res, err := Execute(context.Background(), h.adapter, req, opt)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res, req
}

func (p e2eProject) run(t *testing.T, h e2eHarness, s fakeScript) (Result, fakeRecord) {
	t.Helper()
	rec := installFake(t, h, s)
	res, _ := p.execute(t, h)
	return res, rec
}

const e2eVerifyYAML = "verify:\n  commands:\n    - go test ./...\n    - make lint && make vet\n"

func argAfter(t *testing.T, args []string, flag string) string {
	t.Helper()
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("%s is not in %q", flag, args)
	}
	return args[i+1]
}

type claudeSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

func claudeRules(t *testing.T, rec fakeRecord) claudeSettings {
	t.Helper()
	var s claudeSettings
	if err := json.Unmarshal([]byte(argAfter(t, rec.args(t), "--settings")), &s); err != nil {
		t.Fatalf("--settings is not JSON: %v", err)
	}
	return s
}

type openCodeRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

func openCodeRules(t *testing.T, rec fakeRecord) []openCodeRule {
	t.Helper()
	content, ok := rec.envValue(t, "OPENCODE_CONFIG_CONTENT")
	if !ok {
		t.Fatal("OPENCODE_CONFIG_CONTENT is not set")
	}
	var c struct {
		Permissions []openCodeRule `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(content), &c); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not JSON: %v", err)
	}
	return c.Permissions
}

func hasRule(rules []openCodeRule, action, resource, effect string) bool {
	return slices.Contains(rules, openCodeRule{action, resource, effect})
}

func TestE2ECleanRun(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		res, _ := p.run(t, h, fakeScript{fixture: "clean"})
		if f := res.Failure(); f != "" {
			t.Errorf("Failure = %q", f)
		}
		o := res.Outcome
		if !o.Finished || !o.Success || o.Text != "All done." || len(o.Denials) != 0 || o.BudgetExceeded {
			t.Errorf("Outcome = %+v", o)
		}
		if res.ExitCode != 0 || res.TimedOut || len(res.Suggestions()) != 0 {
			t.Errorf("res = %+v, suggestions %q", res, res.Suggestions())
		}
	})
}

func TestE2EDeniedShellCommandExitsZeroButFails(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		res, _ := p.run(t, h, fakeScript{fixture: "denied"})
		if res.ExitCode != 0 {
			t.Fatalf("ExitCode = %d", res.ExitCode)
		}
		if f := res.Failure(); !strings.HasPrefix(f, "denied actions (1): ") || !strings.Contains(f, "echo hello > shell.txt") {
			t.Errorf("Failure = %q", f)
		}
		if len(res.Outcome.Denials) != 1 || !res.Outcome.Denials[0].Shell {
			t.Errorf("Denials = %+v", res.Outcome.Denials)
		}
	})
}

func TestE2ESuggestionNamesHeadlessAllow(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		res, _ := p.run(t, h, fakeScript{fixture: "denied"})
		s := res.Suggestions()
		if len(s) != 1 || !strings.Contains(s[0], "headless.allow") || !strings.Contains(s[0], `"echo hello > shell.txt"`) {
			t.Errorf("Suggestions = %q", s)
		}
	})
}

func TestE2EPromptOnlyOnStandardInput(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		_, rec := p.run(t, h, fakeScript{fixture: "clean"})
		if got := rec.read(t, "stdin"); got != e2ePrompt {
			t.Errorf("stdin = %q, want the prompt", got)
		}
		for _, a := range rec.args(t) {
			if strings.Contains(a, "UNIQUE-PROMPT") {
				t.Errorf("the prompt is in the argument %q", a)
			}
		}
		if env := rec.read(t, "env"); strings.Contains(env, "UNIQUE-PROMPT") {
			t.Error("the prompt is in the environment")
		}
	})
}

func TestE2ERestrictedRulesAllowAndDeny(t *testing.T) {
	gitPatterns := []string{"git status *", "git diff *", "git log *", "git show *", "git rev-parse *", "git ls-files *"}
	verifyCommands := []string{"go test ./...", "make lint && make vet", "make lint", "make vet"}

	t.Run("claude", func(t *testing.T) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		_, rec := p.run(t, e2eHarnesses[0], fakeScript{fixture: "clean"})
		args := rec.args(t)
		if got := argAfter(t, args, "--permission-mode"); got != "dontAsk" {
			t.Errorf("permission mode = %q", got)
		}
		if got := argAfter(t, args, "--permission-prompts"); got != "none" {
			t.Errorf("permission prompts = %q", got)
		}
		if got := argAfter(t, args, "--setting-sources"); got != "user,local" {
			t.Errorf("setting sources = %q", got)
		}
		if !slices.Contains(args, "--strict-mcp-config") {
			t.Error("--strict-mcp-config is missing")
		}
		s := claudeRules(t, rec)
		allow := []string{"Edit(./**)", "mcp__ralph__status", "mcp__ralph__unit_get", "mcp__ralph__search"}
		for _, c := range append(slices.Clone(gitPatterns), verifyCommands...) {
			allow = append(allow, "Bash("+c+")")
		}
		for _, want := range allow {
			if !slices.Contains(s.Permissions.Allow, want) {
				t.Errorf("allow lacks %q: %q", want, s.Permissions.Allow)
			}
		}
		for _, bare := range []string{"Bash", "Edit", "Read", "Write", "Bash(*)", "Edit(**)"} {
			if slices.Contains(s.Permissions.Allow, bare) {
				t.Errorf("allow has the broad rule %q", bare)
			}
		}
		for _, want := range []string{"Edit(.git/**)", "Edit(.ralph/**)", "Edit(.claude/**)", "Edit(.mcp.json)"} {
			if !slices.Contains(s.Permissions.Deny, want) {
				t.Errorf("deny lacks %q: %q", want, s.Permissions.Deny)
			}
		}
	})

	t.Run("opencode", func(t *testing.T) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		_, rec := p.run(t, e2eHarnesses[1], fakeScript{fixture: "clean"})
		if v, _ := rec.envValue(t, "OPENCODE_DISABLE_PROJECT_CONFIG"); v != "1" {
			t.Errorf("OPENCODE_DISABLE_PROJECT_CONFIG = %q", v)
		}
		args := rec.args(t)
		if slices.Contains(args, "--auto") || !slices.Contains(args, "--standalone") {
			t.Errorf("args = %q", args)
		}
		rules := openCodeRules(t, rec)
		if len(rules) == 0 || rules[0] != (openCodeRule{"*", "*", "deny"}) {
			t.Fatalf("the first rule is not the catch-all deny: %+v", rules)
		}
		allowEdit := slices.Index(rules, openCodeRule{"edit", "*", "allow"})
		if allowEdit < 0 {
			t.Fatalf("no edit allow: %+v", rules)
		}
		var want []openCodeRule
		for _, c := range append(slices.Clone(gitPatterns), verifyCommands...) {
			want = append(want, openCodeRule{"shell", c, "allow"})
		}
		for _, tool := range []string{"status", "unit_get", "search"} {
			want = append(want, openCodeRule{"ralph_" + tool, "*", "allow"})
		}
		for _, w := range want {
			if !slices.Contains(rules, w) {
				t.Errorf("rules lack %+v", w)
			}
		}
		// The last matching rule wins, so each deny must follow the edit allow.
		for _, resource := range []string{".git/*", "*/.git/*", ".ralph/*", "*/.ralph/*", ".opencode/*", "opencode.json", "opencode.jsonc"} {
			i := slices.Index(rules, openCodeRule{"edit", resource, "deny"})
			if i < 0 {
				t.Errorf("no edit deny for %q", resource)
			} else if i < allowEdit {
				t.Errorf("the edit deny for %q comes before the edit allow", resource)
			}
		}
		for _, r := range rules {
			if r.Effect == "allow" && r.Action == "shell" && (r.Resource == "*" || r.Resource == "") {
				t.Errorf("a broad shell allow: %+v", r)
			}
		}
	})
}

func TestE2EAutoModeFromEnvironment(t *testing.T) {
	env := map[string]string{"RALPH_HEADLESS_PERMISSIONS": "auto"}
	t.Run("claude", func(t *testing.T) {
		p := newE2EProject(t, e2eVerifyYAML, env)
		_, rec := p.run(t, e2eHarnesses[0], fakeScript{fixture: "clean"})
		args := rec.args(t)
		if got := argAfter(t, args, "--permission-mode"); got != "auto" {
			t.Errorf("permission mode = %q", got)
		}
		if got := argAfter(t, args, "--permission-prompts"); got != "none" {
			t.Errorf("permission prompts = %q", got)
		}
		s := claudeRules(t, rec)
		if len(s.Permissions.Allow) != 0 {
			t.Errorf("allow = %q, want none", s.Permissions.Allow)
		}
		for _, want := range []string{"Edit(.git/**)", "Edit(.ralph/**)"} {
			if !slices.Contains(s.Permissions.Deny, want) {
				t.Errorf("deny lacks %q: %q", want, s.Permissions.Deny)
			}
		}
	})
	t.Run("opencode", func(t *testing.T) {
		p := newE2EProject(t, e2eVerifyYAML, env)
		_, rec := p.run(t, e2eHarnesses[1], fakeScript{fixture: "clean"})
		if !slices.Contains(rec.args(t), "--auto") {
			t.Errorf("args = %q, want --auto", rec.args(t))
		}
		rules := openCodeRules(t, rec)
		for _, r := range rules {
			if r.Effect != "deny" || r.Action != "edit" {
				t.Errorf("auto mode has the rule %+v, want only edit denies", r)
			}
		}
		if !hasRule(rules, "edit", ".git/*", "deny") || !hasRule(rules, "edit", ".ralph/*", "deny") {
			t.Errorf("the protected paths are not denied: %+v", rules)
		}
	})
}

func TestE2ECommittedFileCannotSetAutoOrAllow(t *testing.T) {
	yaml := e2eVerifyYAML + "headless:\n  permissions: auto\n  allow:\n    - rm -rf *\n"
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, yaml, nil)
		warned := map[string]bool{}
		for _, w := range p.cfg.Warnings() {
			warned[w.Path] = true
		}
		if !warned["headless.permissions"] || !warned["headless.allow"] {
			t.Errorf("Warnings = %v", p.cfg.Warnings())
		}
		_, rec := p.run(t, h, fakeScript{fixture: "clean"})
		args := rec.args(t)
		var allowed []string
		switch h.name {
		case "claude":
			if got := argAfter(t, args, "--permission-mode"); got != "dontAsk" {
				t.Errorf("permission mode = %q", got)
			}
			allowed = claudeRules(t, rec).Permissions.Allow
			if !slices.Contains(allowed, "Edit(./**)") {
				t.Errorf("the run is not restricted: %q", allowed)
			}
		case "opencode":
			if slices.Contains(args, "--auto") {
				t.Error("--auto was passed")
			}
			rules := openCodeRules(t, rec)
			if rules[0] != (openCodeRule{"*", "*", "deny"}) {
				t.Errorf("the run is not restricted: %+v", rules)
			}
			for _, r := range rules {
				allowed = append(allowed, r.Resource)
			}
		}
		for _, a := range allowed {
			if strings.Contains(a, "rm -rf") {
				t.Errorf("the committed headless.allow reached the rules: %q", a)
			}
		}
	})
}

func TestE2EProviderErrorFails(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		res, _ := p.run(t, h, fakeScript{fixture: "provider_error", exit: 1})
		want := map[string]string{"claude": "529 overloaded_error", "opencode": "provider.free-limit"}[h.name]
		if f := res.Failure(); !strings.HasPrefix(f, "harness error: ") || !strings.Contains(f, want) {
			t.Errorf("Failure = %q, want it to name %q", f, want)
		}
		if res.ExitCode != 1 {
			t.Errorf("ExitCode = %d", res.ExitCode)
		}
	})
}

func TestE2ETruncatedStreamFails(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		res, _ := p.run(t, h, fakeScript{fixture: "truncated"})
		if res.Outcome.Finished || res.Outcome.Success {
			t.Errorf("Outcome = %+v", res.Outcome)
		}
		if f := res.Failure(); f != "the harness ended without reporting a result" {
			t.Errorf("Failure = %q", f)
		}
	})
}

// processGone reports whether pid no longer runs; a zombie that nothing has
// reaped yet counts as gone.
func processGone(pid int) bool {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		rest := string(b[strings.LastIndexByte(string(b), ')')+1:])
		return strings.HasPrefix(strings.TrimSpace(rest), "Z")
	}
	return syscall.Kill(pid, 0) != nil
}

func TestE2ETimeoutKillsProcessGroup(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, "headless:\n  timeout: 1s\n", nil)
		rec := installFake(t, h, fakeScript{fixture: "hang", after: "sleep 60 &\necho $! > '{rec}/child'\nwait"})

		start := time.Now()
		res, _ := p.execute(t, h)
		if !res.TimedOut || res.Failure() != "timed out" {
			t.Errorf("TimedOut = %v, Failure = %q", res.TimedOut, res.Failure())
		}
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("the run took %v", d)
		}
		var pid int
		if _, err := fmt.Sscanf(rec.read(t, "child"), "%d", &pid); err != nil || pid <= 0 {
			t.Fatalf("child pid: %v", err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for !processGone(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if !processGone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("the child process %d outlived the run", pid)
		}
	})
}

func TestE2EBudgetStopsOpenCodeStyleRun(t *testing.T) {
	h := e2eHarnesses[1]
	p := newE2EProject(t, "headless:\n  budget_usd: 0.5\n  timeout: 1m\n", nil)
	rec := installFake(t, h, fakeScript{fixture: "budget", after: "sleep 30\ntouch '{rec}/finished'"})

	start := time.Now()
	res, req := p.execute(t, h)
	if req.BudgetUSD != 0.5 {
		t.Errorf("BudgetUSD = %v", req.BudgetUSD)
	}
	if !res.BudgetStopped || !res.Outcome.BudgetExceeded || res.Failure() != "budget exceeded" {
		t.Errorf("res = %+v, failure %q", res, res.Failure())
	}
	if res.Outcome.Usage.CostUSD <= 0.5 {
		t.Errorf("cost = %v, want the run to have passed the cap", res.Outcome.Usage.CostUSD)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the run took %v, it was not stopped", d)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(rec.path("finished")); err == nil {
		t.Error("the process kept running")
	}
}

func TestE2EBudgetClaudeStyleReportsMaxBudget(t *testing.T) {
	h := e2eHarnesses[0]
	p := newE2EProject(t, "headless:\n  budget_usd: 0.0005\n", nil)
	res, rec := p.run(t, h, fakeScript{fixture: "budget", exit: 1})
	if got := argAfter(t, rec.args(t), "--max-budget-usd"); got != "0.0005" {
		t.Errorf("--max-budget-usd = %q", got)
	}
	if res.BudgetStopped {
		t.Error("ralph stopped a run that stops itself")
	}
	if !res.Outcome.BudgetExceeded || res.Failure() != "budget exceeded" {
		t.Errorf("BudgetExceeded = %v, Failure = %q", res.Outcome.BudgetExceeded, res.Failure())
	}
	if !strings.Contains(res.Outcome.Error, "Reached maximum budget") {
		t.Errorf("Error = %q", res.Outcome.Error)
	}
}

func TestE2EMissingCommandIsError(t *testing.T) {
	forEachHarness(t, func(t *testing.T, h e2eHarness) {
		p := newE2EProject(t, e2eVerifyYAML, nil)
		t.Setenv("PATH", t.TempDir())
		mcp := harness.MCPServer{Name: "ralph", Command: "/opt/ralph/bin/ralph", Args: []string{"mcp"}}
		req, err := NewRequest(p.cfg, p.root, e2ePrompt, mcp)
		if err != nil {
			t.Fatal(err)
		}
		opt, err := NewOptions(p.cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Execute(context.Background(), h.adapter, req, opt)
		if want := fmt.Sprintf("%q was not found on the path", h.name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %s", err, want)
		}
	})
}
