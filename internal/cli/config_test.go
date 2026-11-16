package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/testutil"
)

func inRepo(t *testing.T) *testutil.Repo {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	t.Chdir(repo.Dir)
	return repo
}

func row(t *testing.T, out, setting string) []string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == setting {
			return f
		}
	}
	t.Fatalf("no row for %s in:\n%s", setting, out)
	return nil
}

func TestConfigShowDefaults(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "config", "show")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := row(t, stdout, "log.level"); got[1] != "info" || got[2] != "default" {
		t.Errorf("log.level = %q", got)
	}
	if got := row(t, stdout, "project.id"); got[1] != "(unset)" {
		t.Errorf("project.id = %q", got)
	}
}

func TestConfigShowProjectFile(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/project.yaml", "# comment\nlog:\n  level: debug\n")
	code, stdout, stderr := run(t, "config", "show")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	got := row(t, stdout, "log.level")
	want := filepath.Join(".ralph", "project.yaml") + ":3"
	if got[1] != "debug" || got[2] != "project" || got[3] != want {
		t.Errorf("log.level = %q, want source %s", got, want)
	}
}

func TestConfigShowEnvThenFlag(t *testing.T) {
	inRepo(t)
	t.Setenv("RALPH_LOG_LEVEL", "warn")
	_, stdout, stderr := run(t, "config", "show")
	got := row(t, stdout, "log.level")
	if got[1] != "warn" || got[2] != "env" || got[3] != "RALPH_LOG_LEVEL" {
		t.Errorf("log.level = %q (stderr %q)", got, stderr)
	}
	_, stdout, stderr = run(t, "--log-level", "error", "config", "show")
	got = row(t, stdout, "log.level")
	if got[1] != "error" || got[2] != "flag" || got[3] != "--log-level" {
		t.Errorf("log.level = %q (stderr %q)", got, stderr)
	}
}

func TestConfigShowJSON(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "config", "show", "--json")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	for _, e := range entries {
		if e["setting"] == "log.level" {
			if e["value"] != "info" || e["layer"] != "default" {
				t.Errorf("log.level = %v", e)
			}
			return
		}
	}
	t.Errorf("no log.level entry in %s", stdout)
}

func TestConfigShowOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "config", "show")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := row(t, stdout, "log.level"); got[1] != "info" || got[2] != "default" {
		t.Errorf("log.level = %q", got)
	}
}

func TestConfigShowInvalidFile(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/project.yaml", "log:\n  level: info\nbogus: 1\n")
	code, stdout, stderr := run(t, "config", "show")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr, "ralph: invalid configuration:") {
		t.Errorf("stderr = %q", stderr)
	}
	for _, want := range []string{"project.yaml:3", `"bogus"`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestVersionIgnoresBrokenConfig(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/project.yaml", "bogus: 1\n")
	code, stdout, stderr := run(t, "version")
	if code != 0 || !strings.HasPrefix(stdout, "ralph ") || stderr != "" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestConfigAlonePrintsHelp(t *testing.T) {
	inRepo(t)
	code, stdout, _ := run(t, "config")
	if code != 0 || !strings.Contains(stdout, "show") {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
}

func TestLogLevelDebugLogs(t *testing.T) {
	inRepo(t)
	_, _, stderr := run(t, "--log-level", "debug", "config", "show")
	if !strings.Contains(stderr, "configuration loaded") {
		t.Errorf("stderr = %q, want debug message", stderr)
	}
	_, _, stderr = run(t, "config", "show")
	if strings.Contains(stderr, "configuration loaded") {
		t.Errorf("stderr = %q, want no debug message", stderr)
	}
}
