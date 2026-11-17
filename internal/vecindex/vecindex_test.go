package vecindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var ctx = context.Background()

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "index.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func cv(seq int, v ...float32) ChunkVector {
	return ChunkVector{Seq: seq, Text: fmt.Sprintf("text %d", seq), Excerpt: fmt.Sprintf("excerpt %d", seq), Vector: v}
}

func put(t *testing.T, s *Store, kind, ref, hash string, chunks ...ChunkVector) {
	t.Helper()
	if err := s.Put(ctx, Record{Kind: kind, Ref: ref, Unit: "T-1", Hash: hash}, chunks); err != nil {
		t.Fatal(err)
	}
}

func TestStorePutAndRecords(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "h1", cv(0, 1, 0), cv(1, 0, 1))
	put(t, s, "document", "D-1", "h2", cv(0, 1, 1))
	got, err := s.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[Key]RecordInfo{
		{"task", "T-1"}:     {Hash: "h1", Unit: "T-1", Chunks: 2},
		{"document", "D-1"}: {Hash: "h2", Unit: "T-1", Chunks: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	st, err := s.Stats(ctx)
	if err != nil || st.Records != 2 || st.Chunks != 3 || st.Dimensions != 2 {
		t.Fatalf("stats %+v err %v", st, err)
	}
}

func TestStorePutReplacesWholeRecord(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "old", cv(0, 1, 0), cv(1, 0, 1), cv(2, 1, 1))
	put(t, s, "task", "T-1", "new", cv(0, 0, 1))
	recs, _ := s.Records(ctx)
	if info := recs[Key{"task", "T-1"}]; info.Hash != "new" || info.Chunks != 1 {
		t.Fatalf("record = %+v", info)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 10)
	if err != nil || len(hits) != 1 || hits[0].Hash != "new" || hits[0].Seq != 0 {
		t.Fatalf("hits %+v err %v", hits, err)
	}
}

func TestStorePutIsAtomic(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "old", cv(0, 1, 0), cv(1, 0, 1))
	err := s.Put(ctx, Record{Kind: "task", Ref: "T-1", Hash: "new"}, []ChunkVector{cv(0, 1, 0), cv(0, 0, 1)})
	if err == nil {
		t.Fatal("duplicate seq must fail")
	}
	recs, _ := s.Records(ctx)
	if info := recs[Key{"task", "T-1"}]; info.Hash != "old" || info.Chunks != 2 {
		t.Fatalf("record after failure = %+v", info)
	}
	if st, _ := s.Stats(ctx); st.Chunks != 2 {
		t.Fatalf("chunks = %d", st.Chunks)
	}
}

func TestStoreDelete(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "h", cv(0, 1, 0))
	put(t, s, "task", "T-2", "h", cv(0, 0, 1))
	if err := s.Delete(ctx, "task", "T-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "task", "T-9"); err != nil {
		t.Fatalf("deleting a missing record: %v", err)
	}
	recs, _ := s.Records(ctx)
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	if st, _ := s.Stats(ctx); st.Chunks != 1 {
		t.Fatalf("chunks = %d", st.Chunks)
	}
}

func TestStoreSearchBestFirst(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "h1", cv(0, 0, 1), cv(1, 0.6, 0.8))
	put(t, s, "note", "5", "h2", cv(0, 1, 0))
	hits, err := s.Search(ctx, []float32{1, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, fmt.Sprintf("%s/%s/%d/%.1f", h.Kind, h.Ref, h.Seq, h.Score))
	}
	if want := []string{"note/5/0/1.0", "task/T-1/1/0.6", "task/T-1/0/0.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if h := hits[0]; h.Hash != "h2" || h.Unit != "T-1" || h.Text != "text 0" || h.Excerpt != "excerpt 0" {
		t.Fatalf("hit = %+v", h)
	}
}

func TestStoreSearchLimit(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < 20; i++ {
		put(t, s, "task", fmt.Sprintf("T-%d", i), "h", cv(0, float32(i), 1))
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 3)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits %d err %v", len(hits), err)
	}
	for i, want := range []string{"T-19", "T-18", "T-17"} {
		if hits[i].Ref != want {
			t.Fatalf("hit %d = %s, want %s", i, hits[i].Ref, want)
		}
	}
	if hits, _ := s.Search(ctx, []float32{1, 0}, 0); len(hits) != 0 {
		t.Fatal("limit 0 must give nothing")
	}
}

func TestStoreSearchEmpty(t *testing.T) {
	s, _ := open(t)
	hits, err := s.Search(ctx, []float32{1, 2, 3}, 5)
	if err != nil || hits == nil || len(hits) != 0 {
		t.Fatalf("hits %v err %v", hits, err)
	}
	put(t, s, "task", "T-1", "h", cv(0, 1, 0))
	_ = s.Delete(ctx, "task", "T-1")
	if hits, err := s.Search(ctx, []float32{1, 0}, 5); err != nil || len(hits) != 0 {
		t.Fatalf("after delete: %v %v", hits, err)
	}
}

func TestStoreDimensionsFixedByFirstVector(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "h", cv(0, 1, 0, 0))
	err := s.Put(ctx, Record{Kind: "task", Ref: "T-2", Hash: "h"}, []ChunkVector{cv(0, 1, 0, 0), cv(1, 1, 0)})
	if !errors.Is(err, ErrDimensions) || !strings.Contains(err.Error(), "built with vectors of 3 values") {
		t.Fatalf("err = %v", err)
	}
	err = s.Put(ctx, Record{Kind: "task", Ref: "T-1", Hash: "changed"}, []ChunkVector{cv(0, 1, 0)})
	if !errors.Is(err, ErrDimensions) {
		t.Fatalf("err = %v", err)
	}
	recs, _ := s.Records(ctx)
	if len(recs) != 1 || recs[Key{"task", "T-1"}].Hash != "h" {
		t.Fatalf("records changed: %v", recs)
	}
	if st, _ := s.Stats(ctx); st.Chunks != 1 || st.Dimensions != 3 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStoreFailedFirstPutLeavesDimensionsUnset(t *testing.T) {
	s, _ := open(t)
	if err := s.Put(ctx, Record{Kind: "task", Ref: "T-1"}, []ChunkVector{cv(0, 1, 0), cv(1, 1)}); !errors.Is(err, ErrDimensions) {
		t.Fatalf("err = %v", err)
	}
	if st, _ := s.Stats(ctx); st.Dimensions != 0 || st.Records != 0 {
		t.Fatalf("stats %+v", st)
	}
	put(t, s, "task", "T-1", "h", cv(0, 1, 0, 0, 0))
}

func TestStoreSearchWrongLengthQuery(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-1", "h", cv(0, 1, 0))
	if _, err := s.Search(ctx, []float32{1, 0, 0}, 5); !errors.Is(err, ErrDimensions) {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreNewerSchemaRefused(t *testing.T) {
	s, path := open(t)
	_ = s.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE meta SET value = '2' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreReopenKeepsData(t *testing.T) {
	s, path := open(t)
	put(t, s, "task", "T-1", "h", cv(0, 1, 0))
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if st, _ := s2.Stats(ctx); st.Records != 1 || st.Dimensions != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStoreStatsBytes(t *testing.T) {
	s, _ := open(t)
	before, err := s.Stats(ctx)
	if err != nil || before.Bytes <= 0 {
		t.Fatalf("empty stats %+v err %v", before, err)
	}
	for i := 0; i < 50; i++ {
		put(t, s, "task", fmt.Sprintf("T-%d", i), "h", ChunkVector{Seq: 0, Text: strings.Repeat("x", 500), Vector: make([]float32, 64)})
	}
	after, _ := s.Stats(ctx)
	if after.Bytes <= before.Bytes {
		t.Fatalf("bytes %d -> %d", before.Bytes, after.Bytes)
	}
}

func TestStoreConcurrentReaders(t *testing.T) {
	s, _ := open(t)
	put(t, s, "task", "T-0", "h", cv(0, 1, 0))
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 30; i++ {
			if err := s.Put(ctx, Record{Kind: "task", Ref: fmt.Sprintf("T-%d", i), Hash: "h"}, []ChunkVector{cv(0, 1, float32(i)), cv(1, 0, 1)}); err != nil {
				errs <- err
				return
			}
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				hits, err := s.Search(ctx, []float32{1, 0}, 5)
				if err != nil {
					errs <- err
					return
				}
				if len(hits) == 0 {
					errs <- errors.New("no hits")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
