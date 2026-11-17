// Package vecindex is the derived file of chunk vectors that semantic search scans.
package vecindex

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// ErrDimensions means a vector does not have the length the index was built with.
var ErrDimensions = errors.New("vector length differs from the index")

const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS records (
	kind TEXT, ref TEXT, unit TEXT, hash TEXT, chunks INTEGER, indexed_at TEXT,
	PRIMARY KEY (kind, ref)
);
CREATE TABLE IF NOT EXISTS chunks (
	id INTEGER PRIMARY KEY, kind TEXT, ref TEXT, unit TEXT, seq INTEGER, text TEXT, excerpt TEXT, vector BLOB,
	UNIQUE (kind, ref, seq)
);
CREATE INDEX IF NOT EXISTS chunks_record ON chunks (kind, ref);
`

// Record identifies an indexed record and the content its chunks were made from.
type Record struct {
	Kind, Ref, Unit, Hash string
}

// ChunkVector is one chunk of a record with its vector.
type ChunkVector struct {
	Seq     int
	Text    string
	Excerpt string
	Vector  []float32
}

// Key identifies a record in the index.
type Key struct{ Kind, Ref string }

// RecordInfo is what the index holds about a record.
type RecordInfo struct {
	Hash, Unit string
	Chunks     int
}

// ChunkHit is a chunk found by Search; Hash is its record's.
type ChunkHit struct {
	Kind, Ref, Unit, Hash string
	Seq                   int
	Text, Excerpt         string
	Score                 float64
}

// Stats describes the index.
type Stats struct {
	Records, Chunks, Dimensions int
	Bytes                       int64
}

// Store is an open index file.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the index at path, creating the file and its tables.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("vecindex: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("vecindex: create directory for %q: %w", abs, err)
	}
	q := url.Values{
		"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)"},
		"_txlock": {"immediate"},
	}
	u := url.URL{Path: abs}
	sdb, err := sql.Open("sqlite", "file:"+u.EscapedPath()+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("vecindex: open %q: %w", abs, err)
	}
	s := &Store{db: sdb, path: abs}
	if err := s.init(context.Background()); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("vecindex: prepare %q: %w", s.path, err)
	}
	v, ok, err := s.meta(ctx, "schema")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value) VALUES ('schema', ?)`, strconv.Itoa(schemaVersion)); err != nil {
			return fmt.Errorf("vecindex: prepare %q: %w", s.path, err)
		}
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("vecindex: %q has an unreadable schema version %q", s.path, v)
	}
	if n > schemaVersion {
		return fmt.Errorf("vecindex: %q has schema %d, which is newer than the schema %d this ralph reads; update ralph", s.path, n, schemaVersion)
	}
	return nil
}

func (s *Store) meta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("vecindex: read %s: %w", key, err)
	}
	return v, true, nil
}

// Close closes the file.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) dimensions(ctx context.Context) (int, error) {
	v, ok, err := s.meta(ctx, "dimensions")
	if err != nil || !ok {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("vecindex: unreadable dimensions %q", v)
	}
	return n, nil
}

// Put replaces the record and all its chunks in one transaction.
func (s *Store) Put(ctx context.Context, rec Record, chunks []ChunkVector) (err error) {
	for _, c := range chunks {
		if len(c.Vector) == 0 {
			return fmt.Errorf("vecindex: chunk %d of %s %s has no vector", c.Seq, rec.Kind, rec.Ref)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var dims int
	var stored string
	switch qerr := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'dimensions'`).Scan(&stored); {
	case errors.Is(qerr, sql.ErrNoRows):
	case qerr != nil:
		return fmt.Errorf("vecindex: %w", qerr)
	default:
		if dims, err = strconv.Atoi(stored); err != nil {
			return fmt.Errorf("vecindex: unreadable dimensions %q", stored)
		}
	}
	if dims == 0 && len(chunks) > 0 {
		dims = len(chunks[0].Vector)
		if _, err = tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('dimensions', ?)`, strconv.Itoa(dims)); err != nil {
			return fmt.Errorf("vecindex: %w", err)
		}
	}
	for _, c := range chunks {
		if len(c.Vector) != dims {
			err = fmt.Errorf("%w: the index was built with vectors of %d values, but chunk %d of %s %s has %d; it was made with another model, so rebuild the index", ErrDimensions, dims, c.Seq, rec.Kind, rec.Ref, len(c.Vector))
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM chunks WHERE kind = ? AND ref = ?`, rec.Kind, rec.Ref); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO records (kind, ref, unit, hash, chunks, indexed_at) VALUES (?, ?, ?, ?, ?, ?)`,
		rec.Kind, rec.Ref, rec.Unit, rec.Hash, len(chunks), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	for _, c := range chunks {
		if _, err = tx.ExecContext(ctx, `INSERT INTO chunks (kind, ref, unit, seq, text, excerpt, vector) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			rec.Kind, rec.Ref, rec.Unit, c.Seq, c.Text, c.Excerpt, encode(c.Vector)); err != nil {
			return fmt.Errorf("vecindex: store chunk %d of %s %s: %w", c.Seq, rec.Kind, rec.Ref, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	return nil
}

// Delete removes a record and its chunks; a record that is not there is not an error.
func (s *Store) Delete(ctx context.Context, kind, ref string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM chunks WHERE kind = ? AND ref = ?`, kind, ref); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind = ? AND ref = ?`, kind, ref); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("vecindex: %w", err)
	}
	return nil
}

// Records lists what the index holds.
func (s *Store) Records(ctx context.Context) (map[Key]RecordInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, ref, unit, hash, chunks FROM records`)
	if err != nil {
		return nil, fmt.Errorf("vecindex: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[Key]RecordInfo{}
	for rows.Next() {
		var k Key
		var info RecordInfo
		if err := rows.Scan(&k.Kind, &k.Ref, &info.Unit, &info.Hash, &info.Chunks); err != nil {
			return nil, fmt.Errorf("vecindex: %w", err)
		}
		out[k] = info
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vecindex: %w", err)
	}
	return out, nil
}

// Stats counts the records and chunks and measures the file.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM records), (SELECT COUNT(*) FROM chunks)`).Scan(&st.Records, &st.Chunks); err != nil {
		return Stats{}, fmt.Errorf("vecindex: %w", err)
	}
	dims, err := s.dimensions(ctx)
	if err != nil {
		return Stats{}, err
	}
	st.Dimensions = dims
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(s.path + suffix); err == nil {
			st.Bytes += fi.Size()
		}
	}
	return st, nil
}

type scored struct {
	id    int64
	score float64
	hit   ChunkHit
}

// worse reports whether a ranks below b: a lower score, or the same score and a later chunk.
func worse(a, b scored) bool {
	if a.score != b.score {
		return a.score < b.score
	}
	return a.id > b.id
}

type minHeap []scored

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return worse(h[i], h[j]) }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(scored)) }
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Search returns the limit chunks whose vectors have the highest dot product with query, best first.
func (s *Store) Search(ctx context.Context, query []float32, limit int) ([]ChunkHit, error) {
	dims, err := s.dimensions(ctx)
	if err != nil {
		return nil, err
	}
	if dims == 0 {
		return []ChunkHit{}, nil
	}
	if len(query) != dims {
		return nil, fmt.Errorf("%w: the index was built with vectors of %d values, but the query has %d", ErrDimensions, dims, len(query))
	}
	if limit <= 0 {
		return []ChunkHit{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, c.kind, c.ref, c.unit, r.hash, c.seq, c.text, c.excerpt, c.vector
FROM chunks c JOIN records r ON r.kind = c.kind AND r.ref = c.ref`)
	if err != nil {
		return nil, fmt.Errorf("vecindex: %w", err)
	}
	defer func() { _ = rows.Close() }()

	h := &minHeap{}
	for rows.Next() {
		var (
			id                                        int64
			seq                                       int
			kind, ref, unit, hash, text, excerpt, vec sql.RawBytes
		)
		if err := rows.Scan(&id, &kind, &ref, &unit, &hash, &seq, &text, &excerpt, &vec); err != nil {
			return nil, fmt.Errorf("vecindex: %w", err)
		}
		score, ok := dot(query, vec)
		if !ok {
			return nil, fmt.Errorf("vecindex: chunk %d has a vector of %d bytes, not %d; rebuild the index", id, len(vec), 4*dims)
		}
		cand := scored{id: id, score: score}
		if h.Len() == limit && !worse(h.peek(), cand) {
			continue
		}
		cand.hit = ChunkHit{
			Kind: string(kind), Ref: string(ref), Unit: string(unit), Hash: string(hash), Seq: seq,
			Text: string(text), Excerpt: string(excerpt), Score: score,
		}
		if h.Len() == limit {
			(*h)[0] = cand
			heap.Fix(h, 0)
		} else {
			heap.Push(h, cand)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vecindex: %w", err)
	}
	all := []scored(*h)
	slices.SortFunc(all, func(a, b scored) int {
		switch {
		case worse(b, a):
			return -1
		case worse(a, b):
			return 1
		}
		return 0
	})
	out := make([]ChunkHit, len(all))
	for i, c := range all {
		out[i] = c.hit
	}
	return out, nil
}

func (h minHeap) peek() scored { return h[0] }

func encode(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func dot(q []float32, blob []byte) (float64, bool) {
	if len(blob) != 4*len(q) {
		return 0, false
	}
	var sum float64
	for i, x := range q {
		sum += float64(x) * float64(math.Float32frombits(binary.LittleEndian.Uint32(blob[4*i:])))
	}
	return sum, true
}
