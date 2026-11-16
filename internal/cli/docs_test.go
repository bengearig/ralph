package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func TestParseAge(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"90d", 90 * 24 * time.Hour, true},
		{"1d", 24 * time.Hour, true},
		{"36h", 36 * time.Hour, true},
		{"720h", 720 * time.Hour, true},
		{"15m", 15 * time.Minute, true},
		{"0d", 0, false},
		{"0", 0, false},
		{"-1d", 0, false},
		{"-5h", 0, false},
		{"d", 0, false},
		{"1.5d", 0, false},
		{"", 0, false},
		{"99999999999d", 0, false},
	}
	for _, tt := range tests {
		got, err := parseAge(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseAge(%q) = %v, %v; want %v ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

type docsEnv struct {
	t    *testing.T
	repo *testutil.Repo
	ctx  context.Context
}

func newDocsEnv(t *testing.T) *docsEnv {
	t.Helper()
	repo := inRepo(t)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	return &docsEnv{t: t, repo: repo, ctx: units.WithActor(context.Background(), "Ann <ann@example.com>")}
}

func (e *docsEnv) with(fn func(d *db.DB, svc documents.Service)) {
	e.t.Helper()
	d, err := db.Open(e.ctx, filepath.Join(e.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fn(d, documents.NewService(d))
}

// seed creates a document with the given number of earlier versions, each
// body padded to size bytes.
func (e *docsEnv) seed(earlier, size int) string {
	e.t.Helper()
	var id string
	e.with(func(_ *db.DB, svc documents.Service) {
		d, err := svc.Create(e.ctx, documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: strings.Repeat("a", size)})
		if err != nil {
			e.t.Fatal(err)
		}
		id = d.ID
		for i := 0; i < earlier; i++ {
			body := strings.Repeat(string(rune('b'+i)), size)
			if d, err = svc.Update(e.ctx, d.ID, d.Fingerprint, documents.Change{Body: &body}); err != nil {
				e.t.Fatal(err)
			}
		}
	})
	return id
}

func (e *docsEnv) versions(id string) []int {
	e.t.Helper()
	var out []int
	e.with(func(_ *db.DB, svc documents.Service) {
		vs, err := svc.Versions(e.ctx, id)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, v := range vs {
			out = append(out, v.Version)
		}
	})
	return out
}

func TestDocsHelpListsPurge(t *testing.T) {
	code, out, _ := run(t, "docs")
	if code != 0 || !strings.Contains(out, "purge") {
		t.Fatalf("code %d out %q", code, out)
	}
	code, out, _ = run(t, "docs", "--help")
	if code != 0 || !strings.Contains(out, "purge") {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestDocsPurgeNeedsCriteria(t *testing.T) {
	newDocsEnv(t)
	code, out, stderr := run(t, "docs", "purge", "--yes")
	if code != 1 || out != "" || stderr != "ralph: say what to purge: give --keep N, --older-than DURATION or both\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestDocsPurgeDryRunByDefault(t *testing.T) {
	e := newDocsEnv(t)
	a := e.seed(3, 100)
	b := e.seed(2, 50)
	code, out, stderr := run(t, "docs", "purge", "--keep", "0")
	want := fmt.Sprintf("%s  versions 1, 2, 3  300 bytes\n%s  versions 1, 2  100 bytes\n", a, b) +
		"dry run: versions 5, bytes 400, documents 2; nothing was removed; run again with --yes to remove them\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if got := e.versions(a); len(got) != 3 {
		t.Errorf("versions = %v", got)
	}
}

func TestDocsPurgeYesRemoves(t *testing.T) {
	e := newDocsEnv(t)
	a := e.seed(3, 100)
	b := e.seed(2, 50)
	code, out, stderr := run(t, "docs", "purge", "--keep", "1", "--yes")
	want := fmt.Sprintf("%s  versions 1, 2  200 bytes\n%s  versions 1  50 bytes\n", a, b) + "removed: versions 3, bytes 250, documents 2\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if got := e.versions(a); len(got) != 1 || got[0] != 3 {
		t.Errorf("versions = %v", got)
	}
}

func TestDocsPurgeNothing(t *testing.T) {
	e := newDocsEnv(t)
	e.seed(2, 10)
	for _, args := range [][]string{{"--keep", "5"}, {"--keep", "5", "--yes"}} {
		code, out, _ := run(t, append([]string{"docs", "purge"}, args...)...)
		if code != 0 || out != "nothing to purge\n" {
			t.Errorf("%v: code %d out %q", args, code, out)
		}
	}
}

func TestDocsPurgeDocumentFlag(t *testing.T) {
	e := newDocsEnv(t)
	a := e.seed(2, 10)
	b := e.seed(2, 10)
	code, out, _ := run(t, "docs", "purge", "--keep", "0", "--document", b, "--yes")
	if code != 0 || strings.Contains(out, a) || !strings.HasPrefix(out, b+"  versions 1, 2  20 bytes\n") {
		t.Fatalf("code %d out %q", code, out)
	}
	if got := e.versions(a); len(got) != 2 {
		t.Errorf("other document versions = %v", got)
	}
	code, _, stderr := run(t, "docs", "purge", "--keep", "0", "--document", "D-99")
	if code != 1 || !strings.Contains(stderr, "D-99") {
		t.Errorf("unknown: code %d stderr %q", code, stderr)
	}
	code, _, stderr = run(t, "docs", "purge", "--keep", "0", "--document", "nope")
	if code != 1 || !strings.HasPrefix(stderr, "ralph: ") {
		t.Errorf("malformed: code %d stderr %q", code, stderr)
	}
}

func TestDocsPurgeOlderThanDays(t *testing.T) {
	e := newDocsEnv(t)
	id := e.seed(3, 10)
	e.with(func(d *db.DB, _ documents.Service) {
		old := time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := d.SQL().Exec("UPDATE document_versions SET created_at = ? WHERE version <= 2", old); err != nil {
			t.Fatal(err)
		}
	})
	code, out, _ := run(t, "docs", "purge", "--older-than", "90d", "--yes")
	want := id + "  versions 1, 2  20 bytes\nremoved: versions 2, bytes 20, documents 1\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	if got := e.versions(id); len(got) != 1 || got[0] != 3 {
		t.Errorf("versions = %v", got)
	}
}

func TestDocsPurgeJSON(t *testing.T) {
	e := newDocsEnv(t)
	id := e.seed(2, 10)
	type result struct {
		DryRun    bool `json:"dry_run"`
		Documents []struct {
			ID       string `json:"id"`
			Versions []int  `json:"versions"`
			Bytes    int    `json:"bytes"`
		} `json:"documents"`
		Versions int `json:"versions"`
		Bytes    int `json:"bytes"`
	}
	for _, yes := range []bool{false, true} {
		args := []string{"docs", "purge", "--keep", "0", "--json"}
		if yes {
			args = append(args, "--yes")
		}
		code, out, stderr := run(t, args...)
		var got result
		if code != 0 || stderr != "" {
			t.Fatalf("yes=%v code %d stderr %q", yes, code, stderr)
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("yes=%v: %v in %q", yes, err, out)
		}
		if got.DryRun == yes || len(got.Documents) != 1 || got.Documents[0].ID != id ||
			len(got.Documents[0].Versions) != 2 || got.Documents[0].Bytes != 20 || got.Versions != 2 || got.Bytes != 20 {
			t.Errorf("yes=%v result = %+v", yes, got)
		}
	}
	code, out, _ := run(t, "docs", "purge", "--keep", "0", "--json", "--yes")
	if code != 0 || !strings.Contains(out, `"documents": []`) {
		t.Errorf("empty: code %d out %q", code, out)
	}
}

func TestDocsPurgeCompactsAfterRemoval(t *testing.T) {
	e := newDocsEnv(t)
	e.seed(8, 200_000)
	path := filepath.Join(e.repo.Dir, ".ralph", "store.db")
	before := fileSize(t, path)
	code, out, _ := run(t, "docs", "purge", "--keep", "0", "--yes")
	if code != 0 || !strings.Contains(out, "removed: versions 8") {
		t.Fatalf("code %d out %q", code, out)
	}
	after := fileSize(t, path)
	if after >= before/2 {
		t.Errorf("database file is %d bytes after purge, was %d", after, before)
	}
}

func TestDocsPurgeNotInitialized(t *testing.T) {
	inRepo(t)
	code, _, stderr := run(t, "docs", "purge", "--keep", "0")
	if code != 1 || stderr != "ralph: this project has no ralph database: run \"ralph init\" first\n" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestDocsPurgeOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "docs", "purge", "--keep", "0")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestDocsPurgeBadKeepValue(t *testing.T) {
	e := newDocsEnv(t)
	id := e.seed(2, 10)
	for _, v := range []string{"-1", "abc", "1.5", ""} {
		code, out, stderr := run(t, "docs", "purge", "--keep", v, "--yes")
		if code != 1 || out != "" || !strings.HasPrefix(stderr, "ralph: ") {
			t.Errorf("--keep %q: code %d out %q stderr %q", v, code, out, stderr)
		}
	}
	for _, v := range []string{"0d", "0", "-1d", "d", "1.5d", ""} {
		code, out, stderr := run(t, "docs", "purge", "--older-than", v, "--yes")
		if code != 1 || out != "" || !strings.HasPrefix(stderr, "ralph: ") {
			t.Errorf("--older-than %q: code %d out %q stderr %q", v, code, out, stderr)
		}
	}
	if got := e.versions(id); len(got) != 2 {
		t.Errorf("versions = %v", got)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
