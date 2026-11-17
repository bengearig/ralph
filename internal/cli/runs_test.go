package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/testutil"
)

func seedRun(t *testing.T, root, slice, name, task string, age time.Duration, extra int) string {
	t.Helper()
	dir := filepath.Join(root, ".ralph", "runs", slice, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-age).UTC().Truncate(time.Second)
	body := fmt.Sprintf(`{"slice":%q,"task":%q,"started_at":%q}`, slice, task, started.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if extra > 0 {
		if err := os.WriteFile(filepath.Join(dir, "output.jsonl"), []byte(strings.Repeat("x", extra)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

const day = 24 * time.Hour

func TestRunsPurgeNeedsSelector(t *testing.T) {
	inRepo(t)
	code, _, stderr := run(t, "runs", "purge")
	want := "ralph: give --older-than or --slice, so purge never guesses what to remove\n"
	if code != 1 || stderr != want {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestRunsPurgeDryRunByDefault(t *testing.T) {
	repo := inRepo(t)
	old := seedRun(t, repo.Dir, "S-1", "old", "T-1", 100*day, 2048)
	seedRun(t, repo.Dir, "S-1", "new", "T-2", day, 0)
	code, stdout, stderr := run(t, "runs", "purge", "--older-than", "30d")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	info, _ := os.Stat(filepath.Join(old, "record.json"))
	size := fmt.Sprintf("%.1f KiB", float64(2048+info.Size())/1024)
	date := time.Now().Add(-100 * day).UTC().Format(time.DateOnly)
	equalLines(t, stdout,
		".ralph/runs/S-1/old  "+size+"  "+date,
		"1 records, "+size,
		"run again with --yes to remove them",
	)
	if !pathExists(old) {
		t.Error("record removed")
	}
}

func TestRunsPurgeYesRemoves(t *testing.T) {
	repo := inRepo(t)
	old := seedRun(t, repo.Dir, "S-1", "old", "T-1", 100*day, 0)
	keep := seedRun(t, repo.Dir, "S-1", "new", "T-2", day, 0)
	code, stdout, stderr := run(t, "runs", "purge", "--older-than", "30d", "--yes")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "removed 1 records, ") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if pathExists(old) || !pathExists(keep) {
		t.Error("wrong records removed")
	}
}

func TestRunsPurgeNothingMatches(t *testing.T) {
	repo := inRepo(t)
	seedRun(t, repo.Dir, "S-1", "new", "T-1", day, 0)
	for _, args := range [][]string{{"--older-than", "30d"}, {"--older-than", "30d", "--yes"}} {
		code, stdout, _ := run(t, append([]string{"runs", "purge"}, args...)...)
		if code != 0 || stdout != "no run records match\n" {
			t.Errorf("%v: code = %d, stdout = %q", args, code, stdout)
		}
	}
}

func TestRunsPurgeOlderThanDays(t *testing.T) {
	repo := inRepo(t)
	a := seedRun(t, repo.Dir, "S-1", "a", "T-1", 100*day, 0)
	b := seedRun(t, repo.Dir, "S-1", "b", "T-1", 50*day, 0)
	if code, out, _ := run(t, "runs", "purge", "--older-than", "90d", "--yes"); code != 0 || !strings.HasPrefix(out, "removed 1 ") {
		t.Fatalf("90d: %d %q", code, out)
	}
	if pathExists(a) || !pathExists(b) {
		t.Fatal("90d removed the wrong records")
	}
	if code, out, _ := run(t, "runs", "purge", "--older-than", "720h", "--yes"); code != 0 || !strings.HasPrefix(out, "removed 1 ") {
		t.Fatalf("720h: %d %q", code, out)
	}
	if pathExists(b) {
		t.Error("720h kept a 50 day old record")
	}
}

func TestRunsPurgeJSON(t *testing.T) {
	repo := inRepo(t)
	seedRun(t, repo.Dir, "S-1", "old", "T-4", 100*day, 10)
	_, stdout, _ := run(t, "runs", "purge", "--slice", "S-1", "--json")
	var doc struct {
		Records []struct {
			Path      string `json:"path"`
			Slice     string `json:"slice"`
			Task      string `json:"task"`
			StartedAt string `json:"started_at"`
			Bytes     int64  `json:"bytes"`
		} `json:"records"`
		Removed int   `json:"removed"`
		Bytes   int64 `json:"bytes"`
		DryRun  bool  `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err, stdout)
	}
	if len(doc.Records) != 1 || doc.Records[0].Path != ".ralph/runs/S-1/old" || doc.Records[0].Slice != "S-1" ||
		doc.Records[0].Task != "T-4" || doc.Records[0].Bytes <= 10 || doc.Records[0].StartedAt == "" ||
		doc.Removed != 0 || doc.Bytes != doc.Records[0].Bytes || !doc.DryRun {
		t.Errorf("doc = %+v", doc)
	}
	_, stdout, _ = run(t, "runs", "purge", "--slice", "S-1", "--json", "--yes")
	doc.Records = nil
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Removed != 1 || doc.DryRun || len(doc.Records) != 1 {
		t.Errorf("doc = %+v, %v", doc, err)
	}
	_, stdout, _ = run(t, "runs", "purge", "--slice", "S-1", "--json")
	if !strings.Contains(stdout, `"records": []`) {
		t.Errorf("empty list = %q", stdout)
	}
}

func TestRunsPurgeSliceFlagCaseInsensitive(t *testing.T) {
	repo := inRepo(t)
	a := seedRun(t, repo.Dir, "S-1", "a", "T-1", day, 0)
	b := seedRun(t, repo.Dir, "S-2", "b", "T-1", day, 0)
	if code, _, stderr := run(t, "runs", "purge", "--slice", "s-1", "--yes"); code != 0 {
		t.Fatalf("code = %d, %q", code, stderr)
	}
	if pathExists(a) || !pathExists(b) {
		t.Error("wrong records removed")
	}
}

func TestRunsPurgeBadDurationAndSlice(t *testing.T) {
	repo := inRepo(t)
	dir := seedRun(t, repo.Dir, "S-1", "a", "T-1", 100*day, 0)
	for _, args := range [][]string{
		{"--older-than", "soon"}, {"--older-than", "0d"}, {"--slice", "T-1"}, {"--slice", "x"}, {"--slice", "../S-1"},
	} {
		code, _, stderr := run(t, append([]string{"runs", "purge", "--yes"}, args...)...)
		if code != 1 || !strings.HasPrefix(stderr, "ralph: --") {
			t.Errorf("%v: code = %d, stderr = %q", args, code, stderr)
		}
	}
	if !pathExists(dir) {
		t.Error("record removed")
	}
}

func TestRunsPurgeOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "runs", "purge", "--older-than", "1d")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestRunsHelpListsPurge(t *testing.T) {
	inRepo(t)
	code, stdout, _ := run(t, "runs")
	if code != 0 || !strings.Contains(stdout, "purge") {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
	_, root, _ := run(t, "--help")
	if !strings.Contains(root, "runs") {
		t.Errorf("root help = %q", root)
	}
}

func TestLoopAutoTrimSkippedOnDryRun(t *testing.T) {
	e := newLoopEnv(t, "", "First task")
	old := seedRun(t, e.repo.Dir, "S-9", "old", "T-1", 100*day, 0)
	code, stdout, _ := run(t, "loop", "S-1", "--dry-run")
	if code != 0 || strings.Contains(stdout, "removed") || !pathExists(old) {
		t.Errorf("code = %d, stdout = %q, kept = %v", code, stdout, pathExists(old))
	}
}

func TestLoopCommandPrintsInfoEvent(t *testing.T) {
	e := newLoopEnv(t, "", "First task")
	old := seedRun(t, e.repo.Dir, "S-9", "old", "T-1", 100*day, 0)
	code, stdout, _ := run(t, "loop", "S-1")
	if code != 0 || !strings.Contains(stdout, "info: removed 1 run records older than 30 days\n") || pathExists(old) {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
}
