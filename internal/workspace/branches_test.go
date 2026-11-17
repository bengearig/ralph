package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchTipExistsAndMissing(t *testing.T) {
	r, w := newRepo(t)
	r.Git("branch", "topic")
	tip, ok, err := w.BranchTip(ctx, "topic")
	if err != nil || !ok || tip != r.Git("rev-parse", "topic") {
		t.Errorf("topic = %q, %v, %v", tip, ok, err)
	}
	tip, ok, err = w.BranchTip(ctx, "nope")
	if err != nil || ok || tip != "" {
		t.Errorf("nope = %q, %v, %v", tip, ok, err)
	}
}

func TestDeleteBranchRemoves(t *testing.T) {
	r, w := newRepo(t)
	r.Git("switch", "-c", "topic")
	r.Git("commit", "--allow-empty", "-m", "unmerged")
	r.Git("switch", "main")
	if err := w.DeleteBranch(ctx, "topic"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.BranchExists(ctx, "topic"); ok {
		t.Error("topic still exists")
	}
}

func TestDeleteBranchRefusesCheckedOut(t *testing.T) {
	r, w := newRepo(t)
	r.Git("switch", "-c", "topic")
	err := w.DeleteBranch(ctx, "topic")
	if err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Errorf("err = %v", err)
	}
	if ok, _ := w.BranchExists(ctx, "topic"); !ok {
		t.Error("topic was deleted")
	}
}

func TestRemoteBranchTip(t *testing.T) {
	r, w := newRepo(t)
	withRemote(t, r)
	r.Git("switch", "-c", "topic")
	if err := w.Push(ctx, "origin", "topic"); err != nil {
		t.Fatal(err)
	}
	tip, ok, err := w.RemoteBranchTip(ctx, "origin", "topic")
	if err != nil || !ok || tip != r.Git("rev-parse", "topic") {
		t.Errorf("topic = %q, %v, %v", tip, ok, err)
	}
	if _, ok, err := w.RemoteBranchTip(ctx, "origin", "top"); err != nil || ok {
		t.Errorf("prefix matched: %v, %v", ok, err)
	}
	if _, ok, err := w.RemoteBranchTip(ctx, "origin", "nope"); err != nil || ok {
		t.Errorf("nope: %v, %v", ok, err)
	}
	if _, _, err := w.RemoteBranchTip(ctx, "nowhere", "topic"); err == nil {
		t.Error("unknown remote did not fail")
	}
}

func TestDeleteRemoteBranchNeverForces(t *testing.T) {
	r, w := newRepo(t)
	bare := withRemote(t, r)
	r.Git("switch", "-c", "topic")
	if err := w.Push(ctx, "origin", "topic"); err != nil {
		t.Fatal(err)
	}
	if err := w.DeleteRemoteBranch(ctx, "origin", "topic"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.RemoteBranchTip(ctx, "origin", "topic"); ok {
		t.Error("remote branch survived")
	}

	if err := w.Push(ctx, "origin", "topic"); err != nil {
		t.Fatal(err)
	}
	r.Git("-C", bare, "config", "receive.denyDeletes", "true")
	want, _, _ := w.RemoteBranchTip(ctx, "origin", "topic")
	logPath := filepath.Join(t.TempDir(), "args.log")
	wrapper := t.TempDir()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> '" + logPath + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := w.DeleteRemoteBranch(ctx, "origin", "topic"); err == nil {
		t.Error("delete was not refused")
	}
	got, ok, _ := w.RemoteBranchTip(ctx, "origin", "topic")
	if !ok || got != want {
		t.Errorf("remote topic = %q, %v; want %q", got, ok, want)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(logged), "\n") {
		if !strings.Contains(line, "--delete") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if f == "-f" || strings.HasPrefix(f, "--force") || strings.HasPrefix(f, "+") {
				t.Errorf("force option in %q", line)
			}
		}
	}
	if !strings.Contains(string(logged), "--delete") {
		t.Errorf("no delete was logged: %q", logged)
	}
}

func originAhead(t *testing.T) (*Local, string) {
	t.Helper()
	r, w := newRepo(t)
	bare := withRemote(t, r)
	if err := w.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	r.Git("clone", bare, other)
	r.Git("-C", other, "config", "user.name", "Other")
	r.Git("-C", other, "config", "user.email", "o@example.com")
	r.Git("-C", other, "commit", "--allow-empty", "-m", "upstream")
	r.Git("-C", other, "push", "origin", "main")
	return w, r.Dir
}

func TestFastForwardUpdates(t *testing.T) {
	w, dir := originAhead(t)
	changed, err := w.FastForward(ctx, "origin", "main")
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	out, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%s").Output()
	if strings.TrimSpace(string(out)) != "upstream" {
		t.Errorf("head = %q", out)
	}
}

func TestFastForwardUpToDate(t *testing.T) {
	w, _ := originAhead(t)
	if _, err := w.FastForward(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	changed, err := w.FastForward(ctx, "origin", "main")
	if err != nil || changed {
		t.Errorf("changed = %v, err = %v", changed, err)
	}
}

func TestFastForwardRefusesDivergence(t *testing.T) {
	w, dir := originAhead(t)
	r := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "local")
	if out, err := r.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	before, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if _, err := w.FastForward(ctx, "origin", "main"); err == nil {
		t.Fatal("diverged branch was not refused")
	}
	after, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if string(before) != string(after) {
		t.Error("HEAD moved")
	}
}
