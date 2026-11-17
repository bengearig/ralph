package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// writeCommand makes a command that writes to the database and touches no ticket.
func (f *ticketsFixture) writeCommand(s statusDocs) []string {
	return []string{"docs", "link", s.d1, s.task}
}

func (f *ticketsFixture) ticketKeys() int {
	n := 0
	for _, r := range f.srv.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/issue") {
			n++
		}
	}
	return n
}

func (f *ticketsFixture) syncedRows() []tickets.Row {
	f.t.Helper()
	var rows []tickets.Row
	f.store(func(s *tickets.Store) {
		var err error
		if rows, err = s.List(f.ctx); err != nil {
			f.t.Fatal(err)
		}
	})
	return rows
}

// syncedWithEdits syncs the seeded units, then changes the titles of ids.
func (f *ticketsFixture) syncedWithEdits(ids ...string) {
	f.t.Helper()
	if code, _, stderr := run(f.t, "tickets", "sync"); code != 0 {
		f.t.Fatalf("sync: %d %q", code, stderr)
	}
	f.with(func(svc units.Service) {
		for _, id := range ids {
			u, err := svc.Get(f.ctx, id)
			if err != nil {
				f.t.Fatal(err)
			}
			title := "Changed " + id
			if _, err := svc.Update(f.ctx, id, u.Fingerprint, units.Change{Title: &title}); err != nil {
				f.t.Fatal(err)
			}
		}
	})
}

func TestPostRunSyncsAfterWritingCommand(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if got := f.srv.Status("RAL-1"); got == "" {
		t.Fatal("no ticket")
	}
	if n := f.ticketKeys(); n != 2 {
		t.Fatalf("issues created = %d, want the brief and the slice", n)
	}
	if rows := f.syncedRows(); len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPostRunSkipsWhenNoWrites(t *testing.T) {
	f := newTicketsFixture(t)
	f.seedDocs()
	for _, args := range [][]string{{"status"}, {"docs", "links", "T-1"}} {
		if code, _, stderr := run(t, args...); code != 0 {
			t.Fatalf("%v: %d %q", args, code, stderr)
		}
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestPostRunSkipsListedCommands(t *testing.T) {
	f := newTicketsFixture(t)
	f.seedDocs()
	for _, args := range [][]string{{"version"}, {"config", "get", "log.level"}, {"tickets", "sync"}, {"help"}} {
		run(t, args...)
	}
	// tickets sync made its own requests; only it may have.
	creates := f.ticketKeys()
	f.srv.ResetRequests()
	for _, args := range [][]string{{"version"}, {"config", "get", "log.level"}, {"help"}, {"tickets"}} {
		run(t, args...)
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("%d requests from skipped commands", n)
	}
	if creates != 2 {
		t.Fatalf("tickets sync created %d", creates)
	}
}

func mustFind(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find(path)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestPostRunSkipsMCP(t *testing.T) {
	if !skipsAutoSync(mustFind(t, "mcp")) || !skipsAutoSync(mustFind(t, "tickets", "sync")) || !skipsAutoSync(mustFind(t, "config", "get")) {
		t.Fatal("a listed command starts an automatic pass")
	}
	if skipsAutoSync(mustFind(t, "docs", "link")) || skipsAutoSync(mustFind(t, "quick", "start")) {
		t.Fatal("a writing command skips the automatic pass")
	}
}

func TestPostRunNeverChangesExitStatus(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	f.syncedWithEdits(s.brief, s.slice)
	f.srv.FailWith(http.StatusServiceUnavailable, "down")
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 {
		t.Fatalf("code = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, `ralph: warning: 2 tickets failed to sync; run "ralph tickets"`+"\n") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestPostRunWarningCountsAndPlural(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	f.syncedWithEdits(s.slice)
	f.srv.FailWith(http.StatusServiceUnavailable, "down")
	_, _, stderr := run(t, f.writeCommand(s)...)
	if stderr != `ralph: warning: 1 ticket failed to sync; run "ralph tickets"`+"\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestPostRunNoWarningWhenAllGood(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	f.syncedWithEdits()
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestPostRunProviderNoneTouchesNothing(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "none")
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
	if _, err := os.Stat(filepath.Join(project.RalphDir(f.repo.Dir), project.TicketsLockFile)); !os.IsNotExist(err) {
		t.Fatalf("lock file: %v", err)
	}
}

func TestPostRunMissingSiteIsQuiet(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	t.Setenv("RALPH_JIRA_SITE", "")
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestPostRunHonoursSyncTimeout(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	target, err := url.Parse(f.srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer slow.Close()
	defer close(release)
	t.Setenv("RALPH_JIRA_SITE", slow.URL)
	t.Setenv("RALPH_JIRA_SYNC_TIMEOUT", "1s")
	start := time.Now()
	// The run that outlives the wait may still log into the buffer, so the buffer is locked.
	var out, errOut syncBuffer
	code := Run(f.writeCommand(s), &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("command took %v", d)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func TestPostRunRespectsAutoSyncFalse(t *testing.T) {
	f := newTicketsFixture(t)
	s := f.seedDocs()
	t.Setenv("RALPH_JIRA_AUTO_SYNC", "false")
	code, _, stderr := run(t, f.writeCommand(s)...)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestMCPAutoSyncCreatesTicketAfterWriteTool(t *testing.T) {
	f := newTicketsFixture(t)
	a := loadedApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, a, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "unit_create", Arguments: map[string]any{"level": "brief", "title": "From the agent", "body": "b"}})
	if err != nil || res.IsError {
		t.Fatalf("unit_create: %v %v", err, res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.ticketKeys() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stub never got the ticket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	var created int
	d, err := db.Open(f.ctx, filepath.Join(f.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	rows, err := tickets.NewStore(d).List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	created = len(rows)
	if created != 1 {
		t.Fatalf("rows = %d, want 1", created)
	}
}
