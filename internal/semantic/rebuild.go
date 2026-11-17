package semantic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/bengearig/ralph/internal/chunk"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/vecindex"
)

// maxCatchUpPasses is how many further passes a rebuild makes for records that changed while it ran.
const maxCatchUpPasses = 3

const discardLine = "discarding the earlier rebuild: it was started with a different embedding configuration"

// RebuildDeps is what a rebuild reads and writes. Dir holds index.db and index.rebuild.db.
type RebuildDeps struct {
	DB        *db.DB
	Dir       string
	Provider  embed.Provider
	Describer *Describer
	Settings  embed.Settings
	Report    func(string)
	Now       func() time.Time
}

// RebuildPlan is what a rebuild would do.
type RebuildPlan struct {
	Reasons    []string
	Records    int
	Chunks     int
	Done       int
	Resuming   bool
	Discarding bool
	Model, URL string
}

// RebuildResult counts what a finished rebuild holds.
type RebuildResult struct {
	Records, Chunks int
}

func (d RebuildDeps) indexPath() string   { return filepath.Join(d.Dir, project.IndexFile) }
func (d RebuildDeps) rebuildPath() string { return filepath.Join(d.Dir, project.IndexRebuildFile) }

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func removeWithWAL(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := removeFile(p); err != nil {
			return err
		}
	}
	return nil
}

func indexedDone(ctx context.Context, idx *vecindex.Store, recs []Record) (int, error) {
	held, err := idx.Records(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, r := range recs {
		if info, ok := held[r.key()]; ok && info.Hash == r.Hash {
			done++
		}
	}
	return done, nil
}

// RebuildProgress counts the records stored in the rebuild file in dir out of all records; ok is false when no
// rebuild file exists.
func RebuildProgress(ctx context.Context, d *db.DB, dir string) (done, total int, ok bool, err error) {
	path := filepath.Join(dir, project.IndexRebuildFile)
	if ok, err = fileExists(path); err != nil || !ok {
		return 0, 0, false, err
	}
	recs, err := Records(ctx, d)
	if err != nil {
		return 0, 0, false, err
	}
	idx, err := vecindex.Open(path)
	if err != nil {
		return 0, 0, false, err
	}
	defer func() { _ = idx.Close() }()
	done, err = indexedDone(ctx, idx, recs)
	return done, len(recs), true, err
}

// rebuildFile is the state of an existing rebuild file against the current configuration.
type rebuildFile struct {
	exists, discard bool
	done            int
}

func inspectRebuildFile(ctx context.Context, d RebuildDeps, recs []Record) (rebuildFile, error) {
	var st rebuildFile
	ok, err := fileExists(d.rebuildPath())
	if err != nil || !ok {
		return st, err
	}
	st.exists = true
	idx, err := vecindex.Open(d.rebuildPath())
	if err != nil {
		return st, err
	}
	defer func() { _ = idx.Close() }()
	stats, err := idx.Stats(ctx)
	if err != nil {
		return st, err
	}
	if stats.Records == 0 {
		return st, nil
	}
	recorded, has, err := ReadConfig(ctx, idx)
	if err != nil || !has {
		st.discard = true
		return st, nil
	}
	var info embed.Info
	if d.Describer != nil {
		info, _ = d.Describer.Describe(ctx)
	}
	if len(embed.Differences(recorded, embed.NewFingerprint(d.Settings, info))) > 0 {
		st.discard = true
		return st, nil
	}
	st.done, err = indexedDone(ctx, idx, recs)
	return st, err
}

// PlanRebuild reports what a rebuild would do. It embeds nothing and creates no file.
func PlanRebuild(ctx context.Context, d RebuildDeps) (RebuildPlan, error) {
	plan := RebuildPlan{Model: d.Settings.Model, URL: d.Settings.URL}
	recs, err := Records(ctx, d.DB)
	if err != nil {
		return plan, err
	}
	plan.Records = len(recs)
	for _, r := range recs {
		plan.Chunks += len(chunk.Split(chunk.Input{Kind: string(r.Kind), Title: r.Title, Text: r.Text}, d.Settings.ChunkChars))
	}
	ok, err := fileExists(d.indexPath())
	if err != nil {
		return plan, err
	}
	if ok {
		idx, err := vecindex.Open(d.indexPath())
		if err != nil {
			return plan, err
		}
		f, err := Verify(ctx, VerifyDeps{Index: idx, Describer: d.Describer, Settings: d.Settings, Now: d.Now})
		_ = idx.Close()
		if err != nil {
			return plan, err
		}
		if f.Stale {
			plan.Reasons = f.Reasons
		}
	}
	st, err := inspectRebuildFile(ctx, d, recs)
	if err != nil {
		return plan, err
	}
	plan.Discarding = st.discard
	plan.Done = st.done
	plan.Resuming = st.done > 0
	return plan, nil
}

// Rebuild builds a new index beside the old one and replaces the old one when the new one is complete. The old
// index is untouched until then; after an error or a cancelled context the rebuild file is kept so that the
// next call continues.
func Rebuild(ctx context.Context, d RebuildDeps) (RebuildResult, error) {
	release, err := LockIndex(d.Dir)
	if err != nil {
		return RebuildResult{}, err
	}
	defer release()

	recs, err := Records(ctx, d.DB)
	if err != nil {
		return RebuildResult{}, err
	}
	st, err := inspectRebuildFile(ctx, d, recs)
	if err != nil {
		return RebuildResult{}, err
	}
	if st.discard {
		if err := removeWithWAL(d.rebuildPath()); err != nil {
			return RebuildResult{}, fmt.Errorf("remove the earlier rebuild: %w", err)
		}
		if d.Report != nil {
			d.Report(discardLine)
		}
	}

	if err := fillRebuildFile(ctx, d); err != nil {
		return RebuildResult{}, err
	}
	if err := checkpoint(ctx, d.rebuildPath()); err != nil {
		return RebuildResult{}, err
	}
	if err := removeFile(d.indexPath() + "-wal"); err != nil {
		return RebuildResult{}, err
	}
	if err := removeFile(d.indexPath() + "-shm"); err != nil {
		return RebuildResult{}, err
	}
	if err := os.Rename(d.rebuildPath(), d.indexPath()); err != nil {
		return RebuildResult{}, fmt.Errorf("replace the semantic index: %w", err)
	}
	for _, p := range []string{d.rebuildPath() + "-wal", d.rebuildPath() + "-shm"} {
		if err := removeFile(p); err != nil {
			return RebuildResult{}, err
		}
	}
	idx, err := vecindex.Open(d.indexPath())
	if err != nil {
		return RebuildResult{}, err
	}
	defer func() { _ = idx.Close() }()
	stats, err := idx.Stats(ctx)
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Records: stats.Records, Chunks: stats.Chunks}, nil
}

// fillRebuildFile syncs every record into the rebuild file, then again while records changed meanwhile.
func fillRebuildFile(ctx context.Context, d RebuildDeps) error {
	idx, err := vecindex.Open(d.rebuildPath())
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()
	sd := SyncDeps{DB: d.DB, Index: idx, Provider: d.Provider, Settings: d.Settings, Report: d.Report}
	for pass := 0; ; pass++ {
		if _, err := Sync(ctx, sd, SyncOptions{}); err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			return fmt.Errorf("rebuilding the semantic index: %w", err)
		}
		waiting, gone, err := Pending(ctx, sd)
		if err != nil {
			return err
		}
		if len(waiting)+len(gone) == 0 {
			return nil
		}
		if pass == maxCatchUpPasses {
			return errors.New("records keep changing while the semantic index is rebuilt; run ralph index --rebuild --yes again")
		}
	}
}

func checkpoint(ctx context.Context, path string) error {
	u := url.URL{Path: path}
	sdb, err := sql.Open("sqlite", "file:"+u.EscapedPath()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("checkpoint the new semantic index: %w", err)
	}
	defer func() { _ = sdb.Close() }()
	if _, err := sdb.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint the new semantic index: %w", err)
	}
	return nil
}
