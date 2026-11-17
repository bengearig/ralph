package semantic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/chunk"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/core"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/vecindex"
)

func (e *env) dir() string { return filepath.Dir(e.path) }

func (e *env) rebuildDeps(s embed.Settings) RebuildDeps {
	e.reports = nil
	return RebuildDeps{
		DB: e.d, Dir: e.dir(), Provider: e.fake, Describer: NewDescriber(e.fake, nil), Settings: s,
		Report: func(l string) { e.reports = append(e.reports, l) },
	}
}

func (e *env) otherSettings() embed.Settings {
	s := e.settings
	s.ChunkChars = 800
	return s
}

func (e *env) tasks(n int) {
	for i := 0; i < n; i++ {
		e.task("Task", "body", "")
	}
}

func (e *env) rebuildPath() string { return filepath.Join(e.dir(), "index.rebuild.db") }

func (e *env) readIndexFile() []byte {
	e.t.Helper()
	b, err := os.ReadFile(e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) wantFile(path string, want bool) {
	e.t.Helper()
	_, err := os.Stat(path)
	if (err == nil) != want {
		e.t.Errorf("%s exists = %v, want %v", filepath.Base(path), err == nil, want)
	}
}

// partialRebuild stores n records in the rebuild file with the settings s.
func (e *env) partialRebuild(s embed.Settings, n int) {
	e.t.Helper()
	idx, err := vecindex.Open(e.rebuildPath())
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	d := SyncDeps{DB: e.d, Index: idx, Provider: e.fake, Settings: s}
	if _, err := Sync(ctx, d, SyncOptions{MaxRecords: n}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) listing() []string {
	e.t.Helper()
	entries, err := os.ReadDir(e.dir())
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		info, _ := en.Info()
		out = append(out, fmt.Sprintf("%s:%d", en.Name(), info.Size()))
	}
	sort.Strings(out)
	return out
}

func (e *env) totalRecords() int {
	e.t.Helper()
	recs, err := Records(ctx, e.d)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(recs)
}

func TestPlanRebuildWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.tasks(2)
	e.built()
	before, calls := e.listing(), len(e.fake.Calls())
	e.fake.SetInfo(core.Info{Provider: "fake", Model: "fake", Digest: "fake", Dimensions: 16})
	if _, err := PlanRebuild(ctx, e.rebuildDeps(e.settings)); err != nil {
		t.Fatal(err)
	}
	if after := e.listing(); !reflect.DeepEqual(before, after) {
		t.Errorf("files changed: %v -> %v", before, after)
	}
	if got := len(e.fake.Calls()); got != calls {
		t.Errorf("embed calls %d -> %d", calls, got)
	}
	e.wantFile(filepath.Join(e.dir(), "index.lock"), false)
	e.wantFile(e.rebuildPath(), false)
}

func TestPlanRebuildNoIndexFileCreatesNothing(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	d := e.rebuildDeps(e.settings)
	d.Dir = dir
	plan, err := PlanRebuild(ctx, d)
	if err != nil || len(plan.Reasons) != 0 {
		t.Fatalf("plan %+v err %v", plan, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files created: %v", entries)
	}
}

func TestPlanRebuildReasonsAndCounts(t *testing.T) {
	e := newEnv(t)
	e.task("Long", strings.Repeat("word ", 600), "")
	e.doc("Doc", "text")
	e.built()
	s := e.otherSettings()
	s.URL = "http://ollama.test"
	plan, err := PlanRebuild(ctx, e.rebuildDeps(s))
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := Records(ctx, e.d)
	chunks := 0
	for _, r := range recs {
		chunks += len(chunk.Split(chunk.Input{Kind: string(r.Kind), Title: r.Title, Text: r.Text}, 800))
	}
	want := RebuildPlan{Reasons: []string{"chunk size 1200 -> 800"}, Records: len(recs), Chunks: chunks, Model: "nomic-embed-text", URL: "http://ollama.test"}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("plan = %+v, want %+v", plan, want)
	}
	if chunks <= len(recs) {
		t.Errorf("chunks %d should exceed records %d", chunks, len(recs))
	}
}

func TestPlanRebuildNotStaleHasNoReasons(t *testing.T) {
	e := newEnv(t)
	e.built()
	plan, err := PlanRebuild(ctx, e.rebuildDeps(e.settings))
	if err != nil || len(plan.Reasons) != 0 {
		t.Fatalf("plan %+v err %v", plan, err)
	}
}

func TestPlanRebuildResumingCountsDone(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	s := e.otherSettings()
	e.partialRebuild(s, 2)
	plan, err := PlanRebuild(ctx, e.rebuildDeps(s))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Done != 2 || !plan.Resuming || plan.Discarding {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanRebuildDiscardingOnDifferentConfiguration(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	e.partialRebuild(e.otherSettings(), 2)
	plan, err := PlanRebuild(ctx, e.rebuildDeps(e.settings))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Discarding || plan.Done != 0 || plan.Resuming {
		t.Errorf("plan = %+v", plan)
	}

	idx, err := vecindex.Open(e.rebuildPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.DeleteMeta(ctx, metaConfig); err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()
	plan, err = PlanRebuild(ctx, e.rebuildDeps(e.otherSettings()))
	if err != nil || !plan.Discarding {
		t.Errorf("no recorded configuration: plan %+v err %v", plan, err)
	}
}

func TestRebuildReplacesStaleIndex(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.doc("Doc", "text")
	e.built()
	s := e.otherSettings()
	if f := e.inspect(s); !f.Stale {
		t.Fatal("setup: the index is not stale")
	}
	res, err := Rebuild(ctx, e.rebuildDeps(s))
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.index.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != e.totalRecords() || res.Records != st.Records || res.Chunks != st.Chunks {
		t.Errorf("result %+v, index %+v, records %d", res, st, e.totalRecords())
	}
	if f := e.inspect(s); f.Stale || f.Records != res.Records || f.Recorded.ChunkChars != 800 {
		t.Errorf("finding = %+v", f)
	}
	e.wantFile(e.rebuildPath(), false)
	e.wantFile(e.rebuildPath()+"-wal", false)
	e.wantFile(e.rebuildPath()+"-shm", false)
}

func TestRebuildKeepsOldIndexUntilComplete(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	old := e.readIndexFile()
	query := embedtest.Vector("body", 16)
	d := e.rebuildDeps(e.otherSettings())
	seen := 0
	d.Report = func(string) {
		seen++
		if !bytes.Equal(old, e.readIndexFile()) {
			t.Error("the old index file changed during the rebuild")
		}
		hits, err := e.index.Search(ctx, query, 3)
		if err != nil || len(hits) == 0 {
			t.Errorf("old index not searchable: %v %v", hits, err)
		}
	}
	if _, err := Rebuild(ctx, d); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no report")
	}
	if bytes.Equal(old, e.readIndexFile()) {
		t.Error("the index file was not replaced")
	}
}

func TestRebuildProviderFailureKeepsOldAndRebuildFile(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	old := e.readIndexFile()
	boom := errors.New("provider down")
	d := e.rebuildDeps(e.otherSettings())
	n := 0
	d.Report = func(string) {
		if n++; n == 2 {
			e.fake.FailWith(boom)
		}
	}
	_, err := Rebuild(ctx, d)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(old, e.readIndexFile()) {
		t.Error("the old index changed")
	}
	e.wantFile(e.rebuildPath(), true)
	if f := e.inspect(e.settings); f.Stale {
		t.Errorf("old index no longer valid: %+v", f)
	}
}

func TestRebuildResumesWhereItStopped(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	s := e.otherSettings()
	d := e.rebuildDeps(s)
	n := 0
	d.Report = func(string) {
		if n++; n == 2 {
			e.fake.FailWith(errors.New("down"))
		}
	}
	if _, err := Rebuild(ctx, d); err == nil {
		t.Fatal("expected an error")
	}
	e.fake.Recover()
	plan, err := PlanRebuild(ctx, e.rebuildDeps(s))
	if err != nil || plan.Done != 2 || !plan.Resuming {
		t.Fatalf("plan %+v err %v", plan, err)
	}
	calls := len(e.fake.Calls())
	res, err := Rebuild(ctx, e.rebuildDeps(s))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(e.fake.Calls())-calls, e.totalRecords()-2; got != want {
		t.Errorf("second run embedded %d records, want %d", got, want)
	}
	if res.Records != e.totalRecords() {
		t.Errorf("result %+v", res)
	}
}

func TestRebuildDiscardsRebuildFileOfOtherConfiguration(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	e.partialRebuild(e.otherSettings(), 2)
	res, err := Rebuild(ctx, e.rebuildDeps(e.settings))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.reports) == 0 || e.reports[0] != "discarding the earlier rebuild: it was started with a different embedding configuration" {
		t.Errorf("reports = %q", e.reports)
	}
	if f := e.inspect(e.settings); f.Stale || f.Recorded.ChunkChars != 1200 || res.Records != e.totalRecords() {
		t.Errorf("finding %+v result %+v", f, res)
	}
}

func TestRebuildCatchesRecordsChangedMeanwhile(t *testing.T) {
	e := newEnv(t)
	e.tasks(2)
	e.built()
	d := e.rebuildDeps(e.otherSettings())
	var late string
	d.Report = func(string) {
		if late == "" {
			late = e.task("Late", "arrived during the rebuild", "").ID
		}
	}
	res, err := Rebuild(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != e.totalRecords() {
		t.Errorf("records %d, want %d", res.Records, e.totalRecords())
	}
	held, _ := e.index.Records(ctx)
	if _, ok := held[vecindex.Key{Kind: "task", Ref: late}]; !ok {
		t.Errorf("the record added meanwhile is missing: %v", held)
	}
	if waiting, gone, _ := Pending(ctx, e.deps()); len(waiting)+len(gone) != 0 {
		t.Errorf("waiting %d gone %d", len(waiting), len(gone))
	}
}

func TestRebuildGivesUpAfterThreePasses(t *testing.T) {
	e := newEnv(t)
	e.tasks(1)
	e.built()
	old := e.readIndexFile()
	d := e.rebuildDeps(e.otherSettings())
	d.Report = func(string) { e.task("Again", "more", "") }
	_, err := Rebuild(ctx, d)
	if err == nil || !strings.Contains(err.Error(), "records keep changing") || !strings.Contains(err.Error(), "ralph index --rebuild --yes") {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(old, e.readIndexFile()) {
		t.Error("the old index changed")
	}
	e.wantFile(e.rebuildPath(), true)
}

func TestRebuildBusy(t *testing.T) {
	e := newEnv(t)
	e.built()
	release, err := LockIndex(e.dir())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Rebuild(ctx, e.rebuildDeps(e.otherSettings())); !errors.Is(err, ErrIndexBusy) {
		t.Fatalf("err = %v", err)
	}
	e.wantFile(e.rebuildPath(), false)
}

func TestRebuildCancelStopsBetweenRecords(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.built()
	old := e.readIndexFile()
	calls := len(e.fake.Calls())
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := e.rebuildDeps(e.otherSettings())
	d.Report = func(string) { cancel() }
	_, err := Rebuild(cctx, d)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := len(e.fake.Calls()) - calls; got != 1 {
		t.Errorf("embedded %d records after cancel, want 1", got)
	}
	if !bytes.Equal(old, e.readIndexFile()) {
		t.Error("the old index changed")
	}
	e.wantFile(e.rebuildPath(), true)
}

func TestRebuildEmptyProjectCreatesEmptyIndex(t *testing.T) {
	e := newEnv(t)
	e.built()
	e.exec(`DELETE FROM notes`)
	e.exec(`DELETE FROM units`)
	if n := e.totalRecords(); n != 0 {
		t.Fatalf("setup: %d records left", n)
	}
	res, err := Rebuild(ctx, e.rebuildDeps(e.otherSettings()))
	if err != nil || res != (RebuildResult{}) {
		t.Fatalf("result %+v err %v", res, err)
	}
	st, err := e.index.Stats(ctx)
	if err != nil || st.Records != 0 {
		t.Errorf("stats %+v err %v", st, err)
	}
	if f := e.inspect(e.otherSettings()); f.Stale {
		t.Errorf("finding = %+v", f)
	}
}

func TestRebuildRemovesLeftoverWalFiles(t *testing.T) {
	e := newEnv(t)
	e.built()
	if err := e.index.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.path + "-wal", e.path + "-shm"} {
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Rebuild(ctx, e.rebuildDeps(e.otherSettings())); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.path + "-wal", e.path + "-shm", e.rebuildPath(), e.rebuildPath() + "-wal", e.rebuildPath() + "-shm"} {
		e.wantFile(p, false)
	}
	idx, err := vecindex.Open(e.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if st, err := idx.Stats(ctx); err != nil || st.Records == 0 {
		t.Errorf("stats %+v err %v", st, err)
	}
}

func TestRebuildClearsSavedStaleFinding(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.fake.SetInfo(core.Info{Provider: "fake", Model: "fake", Digest: "changed", Dimensions: 16})
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || !f.Stale {
		t.Fatalf("finding %+v err %v", f, err)
	}
	if _, ok := e.savedReason(); !ok {
		t.Fatal("setup: no saved finding")
	}
	if _, err := Rebuild(ctx, e.rebuildDeps(s)); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.savedReason(); ok {
		t.Error("the saved finding survived the rebuild")
	}
	if f := e.inspect(s); f.Stale || f.Recorded.Digest != "changed" {
		t.Errorf("finding = %+v", f)
	}
}

func TestRebuildOpenStoreFollowsNewFile(t *testing.T) {
	e := newEnv(t)
	e.tasks(2)
	e.built()
	late := e.task("Late", "added before the rebuild", "")
	if _, err := Rebuild(ctx, e.rebuildDeps(e.otherSettings())); err != nil {
		t.Fatal(err)
	}
	held, err := e.index.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := held[vecindex.Key{Kind: "task", Ref: late.ID}]; !ok {
		t.Errorf("the open store did not follow the new file: %v", held)
	}
	hits, err := e.index.Search(ctx, embedtest.Vector("added before the rebuild", 16), 1)
	if err != nil || len(hits) != 1 {
		t.Errorf("hits %v err %v", hits, err)
	}
}
