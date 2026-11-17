package semantic

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/embed"
)

func (e *env) summarize(s embed.Settings) Summary {
	e.t.Helper()
	got, err := Summarize(ctx, SummaryDeps{DB: e.d, Dir: e.dir(), Settings: s})
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func TestSummarizeOffWhenNoProvider(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Provider = "none"
	if got := e.summarize(s); got.State != "off" {
		t.Errorf("state = %q, want off", got.State)
	}
}

func TestSummarizeEmptyWhenNoFile(t *testing.T) {
	e := newEnv(t)
	_ = e.index.Close()
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	if got := e.summarize(e.settings); got.State != "empty" {
		t.Errorf("state = %q, want empty", got.State)
	}
	e.wantFile(e.path, false)
}

func TestSummarizeEmptyWhenNoRecords(t *testing.T) {
	e := newEnv(t)
	if got := e.summarize(e.settings); got.State != "empty" {
		t.Errorf("state = %q, want empty", got.State)
	}
}

func TestSummarizeOk(t *testing.T) {
	e := newEnv(t)
	if got := e.summarize(e.built()); got.State != "ok" || len(got.Reasons) != 0 {
		t.Errorf("got %+v, want ok", got)
	}
}

func TestSummarizeStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Model = "other"
	got := e.summarize(s)
	want := e.inspect(s)
	if got.State != "stale" || len(got.Reasons) == 0 || !reflect.DeepEqual(got.Reasons, want.Reasons) {
		t.Errorf("got %+v, want stale with %v", got, want.Reasons)
	}
}

func TestSummarizeLegacyStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	if err := e.index.DeleteMeta(ctx, metaConfig); err != nil {
		t.Fatal(err)
	}
	got := e.summarize(s)
	if got.State != "stale" || !reflect.DeepEqual(got.Reasons, []string{reasonLegacy}) {
		t.Errorf("got %+v, want stale for a legacy index", got)
	}
}

func TestSummarizeRebuildingCounts(t *testing.T) {
	e := newEnv(t)
	e.tasks(4)
	s := e.otherSettings()
	e.partialRebuild(s, 2)
	got := e.summarize(s)
	if got.State != "rebuilding" || got.Done != 2 || got.Total != e.totalRecords() {
		t.Errorf("got %+v, want rebuilding 2 of %d", got, e.totalRecords())
	}
}

func TestSummarizeRebuildingWinsOverStale(t *testing.T) {
	e := newEnv(t)
	e.tasks(3)
	e.mustSync()
	s := e.otherSettings()
	e.partialRebuild(s, 1)
	if got := e.summarize(s); got.State != "rebuilding" || len(got.Reasons) != 0 {
		t.Errorf("got %+v, want rebuilding", got)
	}
}

func TestSummarizeMakesNoProviderCall(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.partialRebuild(s, 1)
	calls := len(e.fake.Calls())
	describes := e.fake.DescribeCalls()
	s.Model = "other"
	e.summarize(s)
	_ = os.Remove(e.rebuildPath())
	e.summarize(s)
	if len(e.fake.Calls()) != calls || e.fake.DescribeCalls() != describes {
		t.Error("Summarize called the provider")
	}
}

func TestSummarizeWritesNothing(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	_ = e.index.Close()
	before := e.listing()
	bytesBefore := e.readIndexFile()
	s.Model = "other"
	e.summarize(s)
	e.summarize(e.settings)
	if !bytes.Equal(bytesBefore, e.readIndexFile()) {
		t.Error("Summarize changed the index file")
	}
	if got := e.listing(); !reflect.DeepEqual(got, before) {
		t.Errorf("directory changed: %v -> %v", before, got)
	}
}

func TestSummarizeUnreadableIndexError(t *testing.T) {
	e := newEnv(t)
	e.built()
	_ = e.index.Close()
	for _, p := range []string{e.path + "-wal", e.path + "-shm"} {
		_ = os.Remove(p)
	}
	if err := os.WriteFile(e.path, []byte(strings.Repeat("not a database ", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Summarize(ctx, SummaryDeps{DB: e.d, Dir: e.dir(), Settings: e.settings}); err == nil {
		t.Error("an unreadable index file gave no error")
	}
}
