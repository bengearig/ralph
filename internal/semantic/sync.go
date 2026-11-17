package semantic

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/bengearig/ralph/internal/chunk"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/vecindex"
)

// SyncDeps is what a sync reads and writes.
type SyncDeps struct {
	DB       *db.DB
	Index    *vecindex.Store
	Provider embed.Provider
	Settings embed.Settings
	Report   func(string)
}

// SyncOptions limits a sync; zero MaxRecords means every waiting record.
type SyncOptions struct {
	MaxRecords int
}

// SyncResult counts what a sync did.
type SyncResult struct {
	Embedded, Chunks, Removed, Waiting int
}

func (r Record) key() vecindex.Key { return vecindex.Key{Kind: string(r.Kind), Ref: r.Ref} }

func (r Record) label() string {
	if r.Kind == search.KindNote {
		return fmt.Sprintf("note N-%s on %s", r.Ref, r.Unit)
	}
	return r.Ref
}

func plan(ctx context.Context, d SyncDeps) (waiting []Record, gone []vecindex.Key, err error) {
	recs, err := Records(ctx, d.DB)
	if err != nil {
		return nil, nil, err
	}
	indexed, err := d.Index.Records(ctx)
	if err != nil {
		return nil, nil, err
	}
	current := make(map[vecindex.Key]Record, len(recs))
	for _, r := range recs {
		current[r.key()] = r
		if info, ok := indexed[r.key()]; !ok || info.Hash != r.Hash {
			waiting = append(waiting, r)
		}
	}
	for k, info := range indexed {
		if r, ok := current[k]; !ok || r.Hash != info.Hash {
			gone = append(gone, k)
		}
	}
	slices.SortFunc(gone, func(a, b vecindex.Key) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Ref, b.Ref))
	})
	return waiting, gone, nil
}

// Pending reports what Sync would do, without writing and without calling the provider.
func Pending(ctx context.Context, d SyncDeps) (waiting []Record, gone []vecindex.Key, err error) {
	return plan(ctx, d)
}

// Sync removes index entries whose record is gone or changed, then embeds the records that are not indexed.
func Sync(ctx context.Context, d SyncDeps, opt SyncOptions) (SyncResult, error) {
	var res SyncResult
	waiting, gone, err := plan(ctx, d)
	if err != nil {
		return res, err
	}
	for _, k := range gone {
		if err := d.Index.Delete(ctx, k.Kind, k.Ref); err != nil {
			return res, err
		}
		res.Removed++
	}
	docPrefix, _ := embed.Prefixes(d.Settings)
	for i, r := range waiting {
		if opt.MaxRecords > 0 && res.Embedded >= opt.MaxRecords {
			res.Waiting = len(waiting) - i
			return res, nil
		}
		if err := ctx.Err(); err != nil {
			res.Waiting = len(waiting) - i
			return res, err
		}
		n, err := embedRecord(ctx, d, r, docPrefix)
		if err != nil {
			res.Waiting = len(waiting) - i
			return res, fmt.Errorf("embedding %s: %w", r.label(), err)
		}
		res.Embedded++
		res.Chunks += n
		if d.Report != nil {
			d.Report(fmt.Sprintf("embedding %s (%d chunks)", r.label(), n))
		}
	}
	return res, nil
}

func embedRecord(ctx context.Context, d SyncDeps, r Record, docPrefix string) (int, error) {
	chunks := chunk.Split(chunk.Input{Kind: string(r.Kind), Title: r.Title, Text: r.Text}, d.Settings.ChunkChars)
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = docPrefix + c.Embed
	}
	vectors, err := d.Provider.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	if len(vectors) != len(chunks) {
		return 0, fmt.Errorf("%w: the provider returned %d vectors for %d texts", embed.ErrInvalid, len(vectors), len(chunks))
	}
	cvs := make([]vecindex.ChunkVector, len(chunks))
	for i, c := range chunks {
		cvs[i] = vecindex.ChunkVector{Seq: c.Seq, Text: c.Text, Excerpt: c.Excerpt, Vector: vectors[i]}
	}
	rec := vecindex.Record{Kind: string(r.Kind), Ref: r.Ref, Unit: r.Unit, Hash: r.Hash}
	if err := d.Index.Put(ctx, rec, cvs); err != nil {
		return 0, err
	}
	return len(chunks), nil
}
