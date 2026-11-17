package opencode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/opencode"
	"github.com/bengearig/ralph/internal/headless"
)

// logHead shows the first lines of the raw output when the test fails, so a
// change in the harness's output is easy to see.
func logHead(t *testing.T, raw *bytes.Buffer) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		lines := strings.Split(raw.String(), "\n")
		if len(lines) > 40 {
			lines = lines[:40]
		}
		for _, l := range lines {
			if len(l) > 600 {
				l = l[:600] + "..."
			}
			t.Log(l)
		}
	})
}

type liveEvent struct {
	Type string `json:"type"`
	Part struct {
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
		} `json:"state"`
	} `json:"part"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func liveEvents(raw string) []liveEvent {
	var out []liveEvent
	for _, line := range strings.Split(raw, "\n") {
		var e liveEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Type != "" {
			out = append(out, e)
		}
	}
	return out
}

// freeLimit is the provider error message when the free model's quota is used.
func freeLimit(raw string) (string, bool) {
	for _, e := range liveEvents(raw) {
		if e.Type == "error" && strings.HasPrefix(e.Error.Type, "provider.free-limit") {
			return e.Error.Type + ": " + e.Error.Message, true
		}
	}
	return "", false
}

func statusCallCompleted(raw string) bool {
	for _, e := range liveEvents(raw) {
		if e.Type == "tool_use" && e.Part.Tool == "ralph_status" && e.Part.State.Status == "completed" {
			return true
		}
	}
	return false
}

const liveHeadlessPrompt = `You are checking what a restricted agent may do. Do these steps in order, one tool call each, and do not stop when one is refused. Do not try other ways around a refusal. After the last step, report the result of every step in one line each.

1. Create the file allowed.txt in the current directory containing "ok", with an editing tool.
2. Run this command with the shell: echo shell > via-shell.txt
3. Run this command with the shell: sh -c 'echo sh > via-sh.txt'
4. Try to create the file .ralph/escape.txt containing "x".
5. Try to write the file .git/hooks/pre-commit containing "#!/bin/sh".
6. Try to write the file OUTSIDE_PATH containing "x".
7. Call the ralph status tool.`

// TestLiveOpenCodeHeadless runs the real OpenCode with a model, in a private
// home. The default model is a free one.
func TestLiveOpenCodeHeadless(t *testing.T) {
	if os.Getenv("RALPH_OPENCODE_LIVE") != "1" {
		t.Skip("set RALPH_OPENCODE_LIVE=1 to run headless against the real OpenCode, which calls a model")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skipf("opencode is not on PATH: %v", err)
	}
	model := os.Getenv("RALPH_OPENCODE_LIVE_MODEL")
	if model == "" {
		model = "opencode/big-pickle"
	}

	ralph := filepath.Join(t.TempDir(), "ralph")
	build := exec.Command("go", "build", "-o", ralph, "github.com/bengearig/ralph/cmd/ralph")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(home, k))
	}

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"git", "init", "-q", repo}, {ralph, "init", "--harness", "opencode"}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(c, " "), err, out)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mcp := harness.MCPServer{Name: "ralph", Command: ralph, Args: []string{"mcp"}}

	load := func(env map[string]string) *config.Config {
		cfg, err := config.Load(config.Options{
			Registry:    config.DefaultRegistry(),
			UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
			ProjectRoot: repo,
			Getenv: func(k string) (string, bool) {
				v, ok := env[k]
				return v, ok
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	execute := func(cfg *config.Config, prompt string) (headless.Result, *bytes.Buffer) {
		req, err := headless.NewRequest(cfg, repo, prompt, mcp)
		if err != nil {
			t.Fatal(err)
		}
		req.Model = model
		opt, err := headless.NewOptions(cfg)
		if err != nil {
			t.Fatal(err)
		}
		opt.Timeout = 8 * time.Minute
		raw := &bytes.Buffer{}
		opt.Stdout = raw
		logHead(t, raw)
		res, err := headless.Execute(context.Background(), opencode.New(), req, opt)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("exit %d, denials %d, failure %q", res.ExitCode, len(res.Outcome.Denials), res.Failure())
		if msg, ok := freeLimit(raw.String()); ok {
			t.Skip(msg)
		}
		return res, raw
	}

	res, raw := execute(load(nil), strings.ReplaceAll(liveHeadlessPrompt, "OUTSIDE_PATH", outside))
	for _, p := range []string{
		filepath.Join(repo, "via-shell.txt"),
		filepath.Join(repo, "via-sh.txt"),
		filepath.Join(repo, ".ralph", "escape.txt"),
		filepath.Join(repo, ".git", "hooks", "pre-commit"),
		outside,
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists: the restricted run wrote it", p)
		}
	}
	if !res.Outcome.Finished {
		t.Errorf("the run did not finish: %+v", res.Outcome)
	}
	if len(res.Outcome.Denials) == 0 {
		t.Error("no denial was reported")
	}
	if res.Failure() == "" {
		t.Error("Failure is empty")
	}
	if !statusCallCompleted(raw.String()) {
		t.Error("the ralph status tool call did not complete without an error")
	}

	execute(load(map[string]string{"RALPH_HEADLESS_PERMISSIONS": "auto"}),
		`Create the file auto.txt in the current directory containing "ok", then say done.`)
	if _, err := os.Stat(filepath.Join(repo, "auto.txt")); err != nil {
		t.Errorf("auto mode did not create auto.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); err == nil {
		t.Error(".git/hooks/pre-commit exists after the auto run")
	}
}
