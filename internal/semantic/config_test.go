package semantic

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/core"
	"github.com/bengearig/ralph/internal/embed/embedtest"
)

func (e *env) config() embed.Fingerprint {
	e.t.Helper()
	fp, ok, err := ReadConfig(ctx, e.index)
	if err != nil || !ok {
		e.t.Fatalf("ReadConfig = %v, %v", ok, err)
	}
	return fp
}

func TestReadConfigNoneRecorded(t *testing.T) {
	e := newEnv(t)
	_, ok, err := ReadConfig(ctx, e.index)
	if ok || err != nil {
		t.Errorf("ReadConfig = %v, %v", ok, err)
	}
}

func TestReadConfigCorrupt(t *testing.T) {
	e := newEnv(t)
	if err := e.index.SetMeta(ctx, metaConfig, "{not json"); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadConfig(ctx, e.index)
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("err = %v", err)
	}
}

func TestRecordConfigWritesBuiltAt(t *testing.T) {
	e := newEnv(t)
	fp := embed.Fingerprint{Provider: "ollama", Model: "m", ChunkChars: 5}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	if err := RecordConfig(ctx, e.index, fp, now); err != nil {
		t.Fatal(err)
	}
	if got := e.config(); got != fp {
		t.Errorf("config = %+v", got)
	}
	if v, _, _ := e.index.Meta(ctx, metaBuiltAt); v != "2026-01-02T02:04:05Z" {
		t.Errorf("built_at = %q", v)
	}
}

func TestSyncRecordsConfigOnFirstRecord(t *testing.T) {
	e := newEnv(t)
	e.task("A", "body", "")
	e.mustSync()
	doc, qry := embed.Prefixes(e.settings)
	want := embed.Fingerprint{Provider: "ollama", Model: "nomic-embed-text", Digest: "fake", Dimensions: 16,
		DocumentPrefix: doc, QueryPrefix: qry, ChunkChars: 1200}
	if got := e.config(); got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
	if _, ok, _ := e.index.Meta(ctx, metaBuiltAt); !ok {
		t.Error("built_at missing")
	}
}

func TestSyncRecordsConfigWithDigestWhenDescribeWorks(t *testing.T) {
	e := newEnv(t)
	e.fake.SetInfo(core.Info{Provider: "ollama", Model: "nomic-embed-text", Digest: "sha-1", Dimensions: 999})
	e.task("A", "body", "")
	e.mustSync()
	if got := e.config(); got.Digest != "sha-1" || got.Dimensions != 16 {
		t.Errorf("config = %+v", got)
	}
	if n := e.fake.DescribeCalls(); n != 1 {
		t.Errorf("DescribeCalls = %d", n)
	}
}

func TestSyncRecordsConfigWhenDescribeFails(t *testing.T) {
	e := newEnv(t)
	e.fake.FailDescribe(errors.New("no tags"))
	e.task("A", "body", "")
	e.mustSync()
	if got := e.config(); got.Digest != "" || got.Dimensions != 16 {
		t.Errorf("config = %+v", got)
	}
}

func TestSyncKeepsRecordedConfigWhenIndexHasRecords(t *testing.T) {
	e := newEnv(t)
	e.task("A", "body", "")
	e.mustSync()
	first := e.config()
	e.settings.ChunkChars = 400
	e.settings.Model = "other"
	e.task("B", "more", "")
	if res := e.mustSync(); res.Embedded == 0 {
		t.Fatal("nothing embedded")
	}
	if got := e.config(); got != first {
		t.Errorf("config = %+v, want %+v", got, first)
	}
}

func TestSyncResetsEmptyIndexBeforeRecording(t *testing.T) {
	e := newEnv(t)
	e.task("A", "body", "")
	e.mustSync()
	e.exec(`DELETE FROM notes`)
	e.exec(`DELETE FROM documents`)
	e.exec(`DELETE FROM units`)
	e.doc("New", "fresh body")
	e.fake = embedtest.NewFake(8)
	e.settings.ChunkChars = 300
	if _, err := e.sync(SyncOptions{}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := e.config()
	if got.Dimensions != 8 || got.ChunkChars != 300 {
		t.Errorf("config = %+v", got)
	}
	if st, _ := e.index.Stats(ctx); st.Dimensions != 8 {
		t.Errorf("stats = %+v", st)
	}
}

func TestSyncDoesNotRecordWhenNothingToEmbed(t *testing.T) {
	e := newEnv(t)
	e.exec(`DELETE FROM units`)
	e.mustSync()
	if _, ok, _ := ReadConfig(ctx, e.index); ok {
		t.Error("config recorded")
	}
	if n := e.fake.DescribeCalls(); n != 0 {
		t.Errorf("DescribeCalls = %d", n)
	}
}
