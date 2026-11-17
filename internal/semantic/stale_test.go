package semantic

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/core"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	_ "modernc.org/sqlite"
)

// built indexes one task with the fake provider and returns the settings it used.
func (e *env) built() embed.Settings {
	e.t.Helper()
	e.task("A", "body", "")
	e.mustSync()
	return e.settings
}

func (e *env) inspect(s embed.Settings) Finding {
	e.t.Helper()
	f, err := Inspect(ctx, e.index, s)
	if err != nil {
		e.t.Fatal(err)
	}
	return f
}

func (e *env) verifyDeps(p embed.Provider, s embed.Settings) VerifyDeps {
	return VerifyDeps{Index: e.index, Describer: NewDescriber(p, nil), Settings: s}
}

func (e *env) savedReason() (string, bool) {
	v, ok, err := e.index.Meta(ctx, metaStaleReason)
	if err != nil {
		e.t.Fatal(err)
	}
	return v, ok
}

func TestInspectEmptyIndexNotStale(t *testing.T) {
	e := newEnv(t)
	if f := e.inspect(e.settings); f.Stale || f.Records != 0 || len(f.Reasons) != 0 {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectNoRecordedConfigIsLegacyStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	if err := e.index.DeleteMeta(ctx, metaConfig); err != nil {
		t.Fatal(err)
	}
	f := e.inspect(s)
	if !f.Stale || f.HasRecorded || len(f.Reasons) != 1 || f.Reasons[0] != "built before its configuration was recorded" || f.Records == 0 {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectCorruptConfigStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	if err := e.index.SetMeta(ctx, metaConfig, "{nope"); err != nil {
		t.Fatal(err)
	}
	f := e.inspect(s)
	if !f.Stale || len(f.Reasons) != 1 || f.Reasons[0] != "its recorded configuration is unreadable" {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectModelChangeReasons(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Model = "mxbai-embed-large"
	s.DocumentPrefix, s.QueryPrefix = e.config().DocumentPrefix, e.config().QueryPrefix
	f := e.inspect(s)
	if !f.Stale || len(f.Reasons) != 1 || f.Reasons[0] != "model nomic-embed-text -> mxbai-embed-large" || !f.HasRecorded {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectPrefixAndChunkChanges(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.DocumentPrefix, s.ChunkChars = "doc: ", 800
	f := e.inspect(s)
	joined := strings.Join(f.Reasons, "|")
	if !f.Stale || !strings.Contains(joined, "document prefix") || !strings.Contains(joined, "chunk size 1200 -> 800") {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectUrlAndTimeoutIgnored(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.URL, s.Timeout = "http://elsewhere:1", 3*time.Second
	if f := e.inspect(s); f.Stale {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectEquivalentConfigNotStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Model += ":latest"
	if f := e.inspect(s); f.Stale {
		t.Errorf("finding = %+v", f)
	}
}

func TestInspectReadsOnly(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Model = "other"
	before, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	e.inspect(s)
	after, _ := os.ReadFile(e.path)
	if !bytes.Equal(before, after) {
		t.Error("Inspect changed the index file")
	}
	if _, ok := e.savedReason(); ok {
		t.Error("Inspect saved a finding")
	}
}

func TestInspectSavedFindingKeepsItStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	if err := e.index.SetMeta(ctx, metaStaleReason, "digest a -> b"); err != nil {
		t.Fatal(err)
	}
	f := e.inspect(s)
	if !f.Stale || len(f.Reasons) != 1 || f.Reasons[0] != "digest a -> b" {
		t.Errorf("finding = %+v", f)
	}
}

func TestVerifyDigestChangeIsStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.fake.SetInfo(core.Info{Provider: "ollama", Model: "nomic-embed-text", Digest: "newdigest", Dimensions: 16})
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || !f.Stale || len(f.Reasons) != 1 || f.Reasons[0] != "digest fake -> newdigest" {
		t.Fatalf("finding = %+v, %v", f, err)
	}
}

func TestVerifyDimensionChangeIsStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.fake.SetInfo(core.Info{Digest: "fake", Dimensions: 32})
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || !f.Stale || len(f.Reasons) != 1 || f.Reasons[0] != "dimensions 16 -> 32" {
		t.Fatalf("finding = %+v, %v", f, err)
	}
}

func TestVerifySavesAndClearsFinding(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	e.fake.SetInfo(core.Info{Digest: "newdigest", Dimensions: 16})
	d := e.verifyDeps(e.fake, s)
	d.Now = func() time.Time { return now }
	if _, err := Verify(ctx, d); err != nil {
		t.Fatal(err)
	}
	if r, ok := e.savedReason(); !ok || r != "digest fake -> newdigest" {
		t.Errorf("stale_reason = %q, %v", r, ok)
	}
	if v, _, _ := e.index.Meta(ctx, metaStaleCheckedAt); v != "2026-03-04T05:06:07Z" {
		t.Errorf("stale_checked_at = %q", v)
	}
	e.fake.SetInfo(core.Info{Digest: "fake", Dimensions: 16})
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || f.Stale {
		t.Fatalf("finding = %+v, %v", f, err)
	}
	if _, ok := e.savedReason(); ok {
		t.Error("stale_reason not cleared")
	}
	if _, ok, _ := e.index.Meta(ctx, metaStaleCheckedAt); ok {
		t.Error("stale_checked_at not cleared")
	}
}

func TestVerifyUnreachableProviderNotStale(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.fake.FailDescribe(embed.ErrUnavailable)
	d := e.verifyDeps(e.fake, s)
	before := e.fake.DescribeCalls()
	for range 3 {
		f, err := Verify(ctx, d)
		if err != nil || f.Stale {
			t.Fatalf("finding = %+v, %v", f, err)
		}
	}
	if n := e.fake.DescribeCalls() - before; n != 1 {
		t.Errorf("describe calls = %d, want 1", n)
	}
}

func TestVerifyUnreachableKeepsSavedFinding(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	if err := e.index.SetMeta(ctx, metaStaleReason, "digest a -> b"); err != nil {
		t.Fatal(err)
	}
	e.fake.FailDescribe(embed.ErrUnavailable)
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || !f.Stale || f.Reasons[0] != "digest a -> b" {
		t.Fatalf("finding = %+v, %v", f, err)
	}
	if r, _ := e.savedReason(); r != "digest a -> b" {
		t.Errorf("saved = %q", r)
	}
}

func TestVerifyEmptyIndexClearsFinding(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{metaStaleReason, metaStaleCheckedAt} {
		if err := e.index.SetMeta(ctx, k, "x"); err != nil {
			t.Fatal(err)
		}
	}
	before := e.fake.DescribeCalls()
	f, err := Verify(ctx, e.verifyDeps(e.fake, e.settings))
	if err != nil || f.Stale {
		t.Fatalf("finding = %+v, %v", f, err)
	}
	if _, ok := e.savedReason(); ok {
		t.Error("stale_reason not cleared")
	}
	if e.fake.DescribeCalls() != before {
		t.Error("an empty index asked the provider")
	}
}

func TestVerifySaveFailureDoesNotFail(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	e.fake.SetInfo(core.Info{Digest: "newdigest", Dimensions: 16})
	other, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.ExecContext(ctx, `CREATE TRIGGER refuse BEFORE INSERT ON meta WHEN NEW.key LIKE 'stale_%' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	f, err := Verify(ctx, e.verifyDeps(e.fake, s))
	if err != nil || !f.Stale {
		t.Fatalf("finding = %+v, %v", f, err)
	}
	if _, ok := e.savedReason(); ok {
		t.Error("the finding was saved despite the trigger")
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type countingProvider struct {
	embed.Provider
	calls int
	err   error
}

func (p *countingProvider) Describe(context.Context) (embed.Info, error) {
	p.calls++
	return embed.Info{Digest: "d"}, p.err
}

func TestDescriberCachesSuccessForTenMinutes(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	p := &countingProvider{}
	d := NewDescriber(p, c.now)
	for range 3 {
		if info, err := d.Describe(ctx); err != nil || info.Digest != "d" {
			t.Fatal(info, err)
		}
	}
	c.t = c.t.Add(10*time.Minute - time.Second)
	_, _ = d.Describe(ctx)
	if p.calls != 1 {
		t.Fatalf("calls = %d", p.calls)
	}
	c.t = c.t.Add(time.Second)
	_, _ = d.Describe(ctx)
	if p.calls != 2 {
		t.Errorf("calls = %d after ten minutes", p.calls)
	}
}

func TestDescriberCachesFailureForSixtySeconds(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	boom := errors.New("boom")
	p := &countingProvider{err: boom}
	d := NewDescriber(p, c.now)
	for range 3 {
		if _, err := d.Describe(ctx); !errors.Is(err, boom) {
			t.Fatal(err)
		}
	}
	c.t = c.t.Add(59 * time.Second)
	if _, err := d.Describe(ctx); !errors.Is(err, boom) || p.calls != 1 {
		t.Fatalf("calls = %d err = %v", p.calls, err)
	}
	c.t = c.t.Add(time.Second)
	_, _ = d.Describe(ctx)
	if p.calls != 2 {
		t.Errorf("calls = %d after a minute", p.calls)
	}
}

func TestDescriberDoesNotCacheCancellation(t *testing.T) {
	fake := embedtest.NewFake(4)
	fake.SetDelay(time.Hour)
	d := NewDescriber(fake, nil)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Describe(cctx); err == nil {
		t.Fatal("expected an error")
	}
	fake.SetDelay(0)
	if _, err := d.Describe(ctx); err != nil {
		t.Errorf("a cancellation was cached: %v", err)
	}
}

func TestSearcherStaleReturnsReasonText(t *testing.T) {
	e := newEnv(t)
	s := e.built()
	s.Model, s.ChunkChars = "other", 800
	s.DocumentPrefix, s.QueryPrefix = e.config().DocumentPrefix, e.config().QueryPrefix
	sr := NewSearcher(SearcherDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: s})
	got, err := sr.Stale(ctx)
	if err != nil || got != "model nomic-embed-text -> other; chunk size 1200 -> 800" {
		t.Errorf("Stale = %q, %v", got, err)
	}
}

func TestSearcherStaleEmptyWhenCurrent(t *testing.T) {
	e := newEnv(t)
	e.built()
	got, err := e.searcher(e.fake).Stale(ctx)
	if err != nil || got != "" {
		t.Errorf("Stale = %q, %v", got, err)
	}
}
