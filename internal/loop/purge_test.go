package loop_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/loop"
)

var purgeNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func mkRecord(t *testing.T, root, slice, name, task string, started time.Time, extra int) string {
	t.Helper()
	dir := filepath.Join(root, ".ralph", "runs", slice, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"slice":%q,"task":%q,"started_at":%q}`, slice, task, started.UTC().Format(time.RFC3339))
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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestListRecordsReadsRecordJSON(t *testing.T) {
	root := t.TempDir()
	started := purgeNow.Add(-time.Hour)
	dir := mkRecord(t, root, "S-1", "20261117-110000-001-T-5", "T-5", started, 100)
	recs, err := loop.ListRecords(root)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "record.json"))
	r := recs[0]
	if r.Path != ".ralph/runs/S-1/20261117-110000-001-T-5" || r.Slice != "S-1" || r.Task != "T-5" ||
		!r.Started.Equal(started) || r.Bytes != 100+info.Size() {
		t.Errorf("record = %+v", r)
	}
}

func TestListRecordsIgnoresOtherEntries(t *testing.T) {
	root := t.TempDir()
	good := mkRecord(t, root, "S-1", "good", "T-1", purgeNow, 0)
	slice := filepath.Join(root, ".ralph", "runs", "S-1")
	if err := os.Mkdir(filepath.Join(slice, "running"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slice, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(slice, "badjson")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "record.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	noStart := filepath.Join(slice, "nostart")
	if err := os.Mkdir(noStart, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noStart, "record.json"), []byte(`{"slice":"S-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(good, filepath.Join(slice, "link")); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := loop.ListRecords(root)
	if err != nil || len(recs) != 1 || !strings.HasSuffix(recs[0].Path, "S-1/good") {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestListRecordsSorted(t *testing.T) {
	root := t.TempDir()
	mkRecord(t, root, "S-2", "b", "T-1", purgeNow, 0)
	mkRecord(t, root, "S-1", "z", "T-1", purgeNow.Add(-time.Hour), 0)
	mkRecord(t, root, "S-1", "a", "T-1", purgeNow, 0)
	recs, err := loop.ListRecords(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range recs {
		got = append(got, filepath.Base(r.Path))
	}
	if strings.Join(got, ",") != "z,a,b" {
		t.Errorf("order = %v", got)
	}
}

func TestListRecordsNoRunsDirectory(t *testing.T) {
	recs, err := loop.ListRecords(t.TempDir())
	if err != nil || len(recs) != 0 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestPurgeRecordsOlderThan(t *testing.T) {
	root := t.TempDir()
	age := 48 * time.Hour
	exact := mkRecord(t, root, "S-1", "exact", "T-1", purgeNow.Add(-age), 0)
	older := mkRecord(t, root, "S-1", "older", "T-1", purgeNow.Add(-age-time.Second), 0)
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{OlderThan: age}, false, purgeNow)
	if err != nil || res.Removed != 1 || len(res.Records) != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if !exists(exact) || exists(older) {
		t.Errorf("exact kept = %v, older kept = %v", exists(exact), exists(older))
	}
}

func TestPurgeRecordsBySlice(t *testing.T) {
	root := t.TempDir()
	a := mkRecord(t, root, "S-1", "a", "T-1", purgeNow, 0)
	b := mkRecord(t, root, "S-2", "b", "T-1", purgeNow, 0)
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: "S-1"}, false, purgeNow)
	if err != nil || res.Removed != 1 || exists(a) || !exists(b) {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestPurgeRecordsBothSelectors(t *testing.T) {
	root := t.TempDir()
	oldA := mkRecord(t, root, "S-1", "old", "T-1", purgeNow.Add(-72*time.Hour), 0)
	newA := mkRecord(t, root, "S-1", "new", "T-1", purgeNow, 0)
	oldB := mkRecord(t, root, "S-2", "old", "T-1", purgeNow.Add(-72*time.Hour), 0)
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: "S-1", OlderThan: 24 * time.Hour}, false, purgeNow)
	if err != nil || res.Removed != 1 || exists(oldA) || !exists(newA) || !exists(oldB) {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestPurgeRecordsDryRunRemovesNothing(t *testing.T) {
	root := t.TempDir()
	dir := mkRecord(t, root, "S-1", "a", "T-1", purgeNow.Add(-72*time.Hour), 50)
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{OlderThan: time.Hour}, true, purgeNow)
	if err != nil || res.Removed != 0 || len(res.Records) != 1 || res.Bytes != res.Records[0].Bytes || !exists(dir) {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestPurgeRecordsRemovesEmptySliceDirectory(t *testing.T) {
	root := t.TempDir()
	mkRecord(t, root, "S-1", "a", "T-1", purgeNow, 0)
	mkRecord(t, root, "S-2", "a", "T-1", purgeNow, 0)
	mkRecord(t, root, "S-2", "b", "T-1", purgeNow, 0)
	runs := filepath.Join(root, ".ralph", "runs")
	if _, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: "S-1"}, false, purgeNow); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(runs, "S-1")) || !exists(filepath.Join(runs, "S-2")) {
		t.Errorf("S-1 exists = %v, S-2 exists = %v", exists(filepath.Join(runs, "S-1")), exists(filepath.Join(runs, "S-2")))
	}
	if _, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: "S-2"}, false, purgeNow); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(runs, "S-2")) || !exists(runs) {
		t.Error("S-2 should be gone and runs kept")
	}
}

func TestPurgeRecordsNeverLeavesRunsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links")
	}
	root := t.TempDir()
	outside := t.TempDir()
	target := mkRecord(t, outside, "S-1", "victim", "T-1", purgeNow.Add(-1000*time.Hour), 10)
	runs := filepath.Join(root, ".ralph", "runs")
	if err := os.MkdirAll(filepath.Join(runs, "S-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(runs, "S-1", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(target), filepath.Join(runs, "S-2")); err != nil {
		t.Fatal(err)
	}
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{OlderThan: time.Hour}, false, purgeNow)
	if err != nil || res.Removed != 0 || !exists(filepath.Join(target, "record.json")) {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestPurgeRecordsRejectsBadSlice(t *testing.T) {
	root := t.TempDir()
	dir := mkRecord(t, root, "S-1", "a", "T-1", purgeNow, 0)
	for _, s := range []string{"s-1", "T-1", "../x", "S-", "S-1/.."} {
		if _, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: s}, false, purgeNow); err == nil {
			t.Errorf("%q was accepted", s)
		}
	}
	if !exists(dir) {
		t.Error("record removed")
	}
}

func lockDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions cannot stop removal here")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestPurgeRecordsContinuesAfterFailure(t *testing.T) {
	root := t.TempDir()
	stuck := mkRecord(t, root, "S-1", "a-stuck", "T-1", purgeNow, 5)
	other := mkRecord(t, root, "S-1", "b-other", "T-1", purgeNow, 5)
	lockDir(t, stuck)
	res, err := loop.PurgeRecords(root, loop.PurgeSelection{Slice: "S-1"}, false, purgeNow)
	if err == nil || !strings.Contains(err.Error(), ".ralph/runs/S-1/a-stuck") {
		t.Fatalf("err = %v", err)
	}
	if res.Removed != 1 || exists(other) || !exists(stuck) {
		t.Errorf("result = %+v", res)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("unexpected not-exist error: %v", err)
	}
}

func autoTrimFixture(t *testing.T, keepDays string) (*prepFixture, string, string) {
	t.Helper()
	p := newPrepFixture(t, 1)
	p.deps.Config = loadConfigEnv(t, p.repo.Dir, map[string]string{config.EnvName("runs.keep_days"): keepDays})
	old := mkRecord(t, p.repo.Dir, "S-9", "old", "T-1", time.Now().Add(-40*24*time.Hour), 0)
	fresh := mkRecord(t, p.repo.Dir, "S-9", "new", "T-1", time.Now().Add(-24*time.Hour), 0)
	return p, old, fresh
}

func TestLoopAutoTrimRemovesOldRecords(t *testing.T) {
	p, old, fresh := autoTrimFixture(t, "30")
	plan := p.prepare(t, false)
	if _, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(fresh) {
		t.Errorf("old kept = %v, new kept = %v", exists(old), exists(fresh))
	}
	var infos []string
	for _, e := range p.events {
		if e.Kind == loop.EventInfo {
			infos = append(infos, e.Message)
		}
	}
	if len(infos) != 1 || infos[0] != "removed 1 run records older than 30 days" {
		t.Errorf("infos = %q", infos)
	}
}

func TestLoopAutoTrimKeepDaysZeroKeepsAll(t *testing.T) {
	p, old, fresh := autoTrimFixture(t, "0")
	plan := p.prepare(t, false)
	if _, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if !exists(old) || !exists(fresh) {
		t.Error("records were removed")
	}
	for _, e := range p.events {
		if e.Kind == loop.EventInfo {
			t.Errorf("unexpected info %q", e.Message)
		}
	}
}

func TestLoopAutoTrimFailureIsWarning(t *testing.T) {
	p, old, _ := autoTrimFixture(t, "30")
	lockDir(t, old)
	plan := p.prepare(t, false)
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if err != nil || sum.Done != 1 {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	var warned bool
	for _, e := range p.events {
		if e.Kind == loop.EventWarning && strings.Contains(e.Message, ".ralph/runs/S-9/old") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("events = %+v", p.events)
	}
}
