package semantic

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/vecindex"
)

const (
	metaStaleReason    = "stale_reason"
	metaStaleCheckedAt = "stale_checked_at"

	reasonLegacy     = "built before its configuration was recorded"
	reasonUnreadable = "its recorded configuration is unreadable"

	describeSuccessTTL = 10 * time.Minute
	describeFailureTTL = 60 * time.Second
)

// Finding is whether the index was built with a configuration other than the current one.
type Finding struct {
	Stale       bool
	Reasons     []string
	Recorded    embed.Fingerprint
	HasRecorded bool
	Records     int
}

// Inspect compares the recorded configuration with the settings. It calls no provider and writes nothing.
func Inspect(ctx context.Context, idx *vecindex.Store, s embed.Settings) (Finding, error) {
	st, err := idx.Stats(ctx)
	if err != nil {
		return Finding{}, err
	}
	f := Finding{Records: st.Records}
	if st.Records == 0 {
		return f, nil
	}
	recorded, ok, err := ReadConfig(ctx, idx)
	if err != nil {
		f.Stale, f.Reasons = true, []string{reasonUnreadable}
		return f, nil
	}
	if !ok {
		f.Stale, f.Reasons = true, []string{reasonLegacy}
		return f, nil
	}
	f.Recorded, f.HasRecorded = recorded, true
	if diff := embed.Differences(recorded, embed.NewFingerprint(s, embed.Info{})); len(diff) > 0 {
		f.Stale, f.Reasons = true, diff
		return f, nil
	}
	saved, ok, err := idx.Meta(ctx, metaStaleReason)
	if err != nil {
		return Finding{}, err
	}
	if ok && saved != "" {
		f.Stale, f.Reasons = true, []string{saved}
	}
	return f, nil
}

// Describer asks a provider to describe its model and caches the answer, a failure for a shorter time.
type Describer struct {
	p   embed.Provider
	now func() time.Time

	mu      sync.Mutex
	info    embed.Info
	err     error
	expires time.Time
}

// NewDescriber returns a Describer over p; a nil now means time.Now.
func NewDescriber(p embed.Provider, now func() time.Time) *Describer {
	if now == nil {
		now = time.Now
	}
	return &Describer{p: p, now: now}
}

// Describe returns the provider's model info, from the cache while it is fresh.
func (d *Describer) Describe(ctx context.Context) (embed.Info, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.expires.IsZero() && d.now().Before(d.expires) {
		return d.info, d.err
	}
	info, err := d.p.Describe(ctx)
	if err != nil && ctx.Err() != nil {
		return embed.Info{}, err
	}
	d.info, d.err = info, err
	ttl := describeSuccessTTL
	if err != nil {
		ttl = describeFailureTTL
	}
	d.expires = d.now().Add(ttl)
	return info, err
}

// VerifyDeps is what Verify reads and writes.
type VerifyDeps struct {
	Index     *vecindex.Store
	Describer *Describer
	Settings  embed.Settings
	Now       func() time.Time
}

// Verify is Inspect plus a comparison with what the provider reports now. A difference is saved in the
// index so that commands that stay offline can show it; an unreachable provider changes nothing.
func Verify(ctx context.Context, d VerifyDeps) (Finding, error) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	f, err := Inspect(ctx, d.Index, d.Settings)
	if err != nil {
		return Finding{}, err
	}
	if f.Records == 0 {
		clearFinding(ctx, d.Index)
		return f, nil
	}
	if !f.HasRecorded || d.Describer == nil {
		return f, nil
	}
	info, err := d.Describer.Describe(ctx)
	if err != nil {
		return f, nil
	}
	diff := embed.Differences(f.Recorded, embed.NewFingerprint(d.Settings, info))
	if len(diff) == 0 {
		clearFinding(ctx, d.Index)
		f.Stale, f.Reasons = false, nil
		return f, nil
	}
	f.Stale, f.Reasons = true, diff
	if err := d.Index.SetMeta(ctx, metaStaleReason, strings.Join(diff, "; ")); err != nil {
		slog.Warn("could not save the stale semantic index finding", "error", err)
		return f, nil
	}
	if err := d.Index.SetMeta(ctx, metaStaleCheckedAt, now().UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("could not save the stale semantic index finding", "error", err)
	}
	return f, nil
}

func clearFinding(ctx context.Context, idx *vecindex.Store) {
	for _, k := range []string{metaStaleReason, metaStaleCheckedAt} {
		if err := idx.DeleteMeta(ctx, k); err != nil {
			slog.Warn("could not clear the stale semantic index finding", "error", err)
		}
	}
}
