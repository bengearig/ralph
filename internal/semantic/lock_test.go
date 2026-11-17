package semantic

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const helperEnv = "RALPH_TEST_LOCK_DIR"

func TestLockIndexExcludes(t *testing.T) {
	dir := t.TempDir()
	release, err := LockIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = LockIndex(dir)
	if !errors.Is(err, ErrIndexBusy) || err.Error() != "another ralph index run is in progress" {
		t.Fatalf("err = %v", err)
	}
}

func TestLockIndexReleaseAllowsAgain(t *testing.T) {
	dir := t.TempDir()
	release, err := LockIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	again, err := LockIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestLockIndexHelperProcess(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("helper process")
	}
	release, err := LockIndex(dir)
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(2)
	}
	defer release()
	_, _ = os.Stdout.WriteString("locked\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestLockIndexAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockIndexHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	finish := func() {
		if !done {
			done = true
			_ = stdin.Close()
			_ = cmd.Wait()
		}
	}
	defer finish()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("helper said %q, %v", line, err)
	}
	if _, err := LockIndex(dir); !errors.Is(err, ErrIndexBusy) {
		t.Fatalf("err = %v", err)
	}
	finish()
	release, err := LockIndex(dir)
	if err != nil {
		t.Fatalf("after the holder ended: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(dir, "index.lock")); err != nil {
		t.Fatal(err)
	}
}

func TestSearcherCatchUpLockedEmbedsNothing(t *testing.T) {
	e := newEnv(t)
	for range 3 {
		e.task("lemur", "text", "")
	}
	dir := t.TempDir()
	s := NewSearcher(SearcherDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: e.settings, Dir: dir})
	release, err := LockIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waiting, err := s.CatchUp(ctx, 10)
	if err != nil || waiting != 5 {
		t.Fatalf("waiting %d err %v", waiting, err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("provider saw %d calls", n)
	}
	st, err := e.index.Stats(ctx)
	if err != nil || st.Records != 0 {
		t.Fatalf("stats %+v err %v", st, err)
	}
}

func TestSearcherCatchUpUnlockedStillEmbeds(t *testing.T) {
	e := newEnv(t)
	e.task("lemur", "text", "")
	dir := t.TempDir()
	s := NewSearcher(SearcherDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: e.settings, Dir: dir})
	waiting, err := s.CatchUp(ctx, 10)
	if err != nil || waiting != 0 {
		t.Fatalf("waiting %d err %v", waiting, err)
	}
	if len(e.fake.Calls()) == 0 {
		t.Fatal("nothing embedded")
	}
	release, err := LockIndex(dir)
	if err != nil {
		t.Fatalf("lock still held after CatchUp: %v", err)
	}
	release()
}
