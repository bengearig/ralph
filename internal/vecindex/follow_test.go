package vecindex

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// replacement builds a second index beside path holding one record, renames it over path and removes the old
// write-ahead files, as a rebuild does.
func replacement(t *testing.T, path, ref string) {
	t.Helper()
	tmp := filepath.Join(filepath.Dir(path), "index.rebuild.db")
	n, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	put(t, n, "task", ref, "h", cv(0, 1, 0))
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

func TestStoreFollowsReplacedFile(t *testing.T) {
	s, path := open(t)
	put(t, s, "task", "T-old", "h", cv(0, 0, 1))
	replacement(t, path, "T-new")
	recs, err := s.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recs[Key{"task", "T-new"}]; !ok || len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 5)
	if err != nil || len(hits) != 1 || hits[0].Ref != "T-new" {
		t.Fatalf("hits = %v, %v", hits, err)
	}
}

func TestStoreFollowsReplacedFileOnPut(t *testing.T) {
	s, path := open(t)
	put(t, s, "task", "T-old", "h", cv(0, 0, 1))
	replacement(t, path, "T-new")
	put(t, s, "task", "T-put", "h", cv(0, 1, 0))
	_ = s.Close()
	check, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	recs, err := check.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[Key{"task", "T-put"}].Hash == "" || recs[Key{"task", "T-new"}].Hash == "" {
		t.Fatalf("records = %v", recs)
	}
}

func TestStoreReopensWhenFileRemoved(t *testing.T) {
	s, path := open(t)
	put(t, s, "task", "T-old", "h", cv(0, 0, 1))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	recs, err := s.Records(ctx)
	if err != nil || len(recs) != 0 {
		t.Fatalf("records = %v, %v", recs, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created again: %v", err)
	}
	if v, ok, _ := s.meta(ctx, "schema"); !ok || v == "" {
		t.Error("tables not created")
	}
	put(t, s, "task", "T-again", "h", cv(0, 1, 0))
}

func TestStoreSameFileDoesNotReopen(t *testing.T) {
	s, _ := open(t)
	before := s.opened
	for range 3 {
		put(t, s, "task", "T-1", "h", cv(0, 1, 0))
		if _, err := s.Records(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if s.opened != before {
		t.Fatalf("opened %d times, want %d", s.opened, before)
	}
}

func TestStoreConcurrentUseDuringReplace(t *testing.T) {
	s, path := open(t)
	put(t, s, "task", "T-old", "h", cv(0, 1, 0))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := s.Records(context.Background()); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.Search(context.Background(), []float32{1, 0}, 3); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := range 3 {
		replacement(t, path, "T-new"+string(rune('a'+i)))
	}
	close(stop)
	wg.Wait()
	recs, err := s.Records(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %v, %v", recs, err)
	}
}
