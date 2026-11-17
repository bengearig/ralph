package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/tickets"
)

const (
	driftLine   = "tickets: 1 with differences in Jira; run ralph tickets\n"
	failureLine = "tickets: 1 failed to sync; run ralph tickets\n"
)

func editTicketRow(t *testing.T, dir, unit string, fn func(*tickets.Row)) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	st := tickets.NewStore(d)
	row, ok, err := st.Get(ctx, unit)
	if err != nil || !ok {
		t.Fatalf("row %s: %v %v", unit, ok, err)
	}
	fn(&row)
	if err := st.Put(ctx, row); err != nil {
		t.Fatal(err)
	}
}

func driftAndFail(t *testing.T, dir string) {
	t.Helper()
	editTicketRow(t, dir, "S-1", func(r *tickets.Row) { r.Drift, r.DriftSeenAt = "title,body", time.Now() })
	editTicketRow(t, dir, "S-2", func(r *tickets.Row) {
		r.FailedStep, r.LastError, r.FailedAt = "update", "boom", time.Now()
	})
}

func TestStatusTicketsLines(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	makeOutdated(t, f.repo.Dir)
	_, base, _ := f.status()
	driftAndFail(t, f.repo.Dir)
	code, out, stderr := f.status()
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	plain := strings.TrimSuffix(base, "\n"+integrationOutdatedLine+"\n")
	want := plain + "\n" + driftLine + failureLine + "\n" + integrationOutdatedLine + "\n"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestStatusTicketsSilentWhenNone(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	_, out, _ := f.status()
	if strings.Contains(out, "tickets:") {
		t.Errorf("out = %q", out)
	}
	driftAndFail(t, f.repo.Dir)
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "none")
	if _, out, _ = f.status(); strings.Contains(out, "tickets:") {
		t.Errorf("provider none: %q", out)
	}
}

func TestStatusTicketsJSON(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	var v map[string]any
	_, out, _ := f.status("--json")
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if _, has := v["tickets"]; has {
		t.Errorf("key present when clean: %v", v)
	}
	driftAndFail(t, f.repo.Dir)
	_, out, _ = f.status("--json")
	var got struct {
		Tickets tickets.Summary `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := []tickets.SummaryUnit{
		{Unit: "S-1", Key: "RAL-2", Kind: "drift", Detail: "title, body"},
		{Unit: "S-2", Key: "RAL-3", Kind: "failure", Detail: "update: boom"},
	}
	if got.Tickets.Drifted != 1 || got.Tickets.Failed != 1 || len(got.Tickets.Units) != 2 || got.Tickets.Units[0] != want[0] || got.Tickets.Units[1] != want[1] {
		t.Errorf("tickets = %+v", got.Tickets)
	}
}

func TestStatusTicketsFailureDoesNotFailCommand(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	_, base, _ := f.status()
	d, err := db.Open(f.ctx, filepath.Join(f.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(f.ctx, "DROP TABLE tickets"); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	code, out, stderr := f.status()
	if code != 0 || out != base || !strings.Contains(stderr, "ticket state") {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func monitorTicketsEnv(t *testing.T) {
	t.Helper()
	e, _ := monitorEnv(t)
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "jira")
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	st := tickets.NewStore(d)
	for i, id := range []string{"S-1", "S-2"} {
		if err := st.Put(context.Background(), tickets.Row{UnitID: id, System: "jira", Key: "RAL-" + string(rune('1'+i)), ID: string(rune('1' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	driftAndFail(t, e.repo.Dir)
}

func TestMonitorTicketsLines(t *testing.T) {
	monitorTicketsEnv(t)
	code, out, stderr := run(t, "monitor", "--no-poll")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	want := "\n" + driftLine + failureLine + "nothing to do\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("out = %q", out)
	}
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "none")
	if _, out, _ = run(t, "monitor", "--no-poll"); strings.Contains(out, "tickets:") {
		t.Errorf("provider none: %q", out)
	}
}

func TestMonitorTicketsJSON(t *testing.T) {
	monitorTicketsEnv(t)
	_, out, _ := run(t, "monitor", "--no-poll", "--json")
	var got struct {
		Tickets *tickets.Summary `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tickets == nil || got.Tickets.Drifted != 1 || got.Tickets.Failed != 1 || len(got.Tickets.Units) != 2 {
		t.Errorf("tickets = %+v", got.Tickets)
	}
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "none")
	_, out, _ = run(t, "monitor", "--no-poll", "--json")
	if strings.Contains(out, `"tickets"`) {
		t.Errorf("key present when none: %s", out)
	}
}

func TestTicketsCommandListDriftAndFailureColumns(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	driftAndFail(t, f.repo.Dir)
	_, out, _ := run(t, "tickets")
	ls := strings.Split(strings.TrimSpace(out), "\n")
	if len(ls) != 3 || !strings.HasSuffix(ls[1], "First slice  (differs in Jira: title, body)") || !strings.HasSuffix(ls[2], "Second slice  (failed update: boom)") || strings.Contains(ls[0], "(") {
		t.Errorf("out = %q", out)
	}
	_, out, _ = run(t, "tickets", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if d, _ := rows[1]["drift"].([]any); len(d) != 2 || d[0] != "title" || d[1] != "body" {
		t.Errorf("drift = %v", rows[1]["drift"])
	}
	if d, ok := rows[0]["drift"].([]any); !ok || len(d) != 0 {
		t.Errorf("clean drift = %v", rows[0]["drift"])
	}
	if rows[2]["failed_step"] != "update" || rows[2]["last_error"] != "boom" {
		t.Errorf("row = %v", rows[2])
	}
}

func TestTicketsNoticeCodeAndMessage(t *testing.T) {
	cases := []struct {
		sum  tickets.Summary
		want string
	}{
		{tickets.Summary{Drifted: 2, Failed: 1}, "Jira tickets need attention: 2 differ from ralph's values (a person edited them in Jira) and 1 failed to sync. Ask the user how to proceed before running ralph tickets accept or overwrite; do not decide for them."},
		{tickets.Summary{Drifted: 3}, "Jira tickets need attention: 3 differ from ralph's values (a person edited them in Jira). Ask the user how to proceed before running ralph tickets accept or overwrite; do not decide for them."},
		{tickets.Summary{Failed: 4}, "Jira tickets need attention: 4 failed to sync. Ask the user how to proceed before running ralph tickets accept or overwrite; do not decide for them."},
	}
	for _, c := range cases {
		if got := ticketsNoticeMessage(c.sum); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	f := newTicketsFixture(t)
	f.syncedTickets()
	driftAndFail(t, f.repo.Dir)
	want := ticketsNoticeMessage(tickets.Summary{Drifted: 1, Failed: 1})
	got, err := ticketsNoticeSource(t, nil)(context.Background())
	if err != nil || len(got) != 1 || got[0].Code != "tickets_need_attention" || got[0].Message != want {
		t.Errorf("got %+v, %v", got, err)
	}
}

func ticketsNoticeSource(t *testing.T, queries *int) mcpserver.NoticeSource {
	t.Helper()
	a := loadedApp(t)
	var d *db.DB
	t.Cleanup(func() {
		if d != nil {
			_ = d.Close()
		}
	})
	store := func(ctx context.Context) (*db.DB, error) {
		if d != nil {
			return d, nil
		}
		var err error
		d, err = openStore(ctx, a, db.Options{MustExist: true})
		return d, err
	}
	if queries == nil {
		return ticketsNotice(a, store)
	}
	return ticketsNoticeWith(a, store, func(ctx context.Context, d *db.DB) (tickets.Summary, error) {
		*queries++
		return tickets.Summarize(ctx, tickets.SummaryDeps{DB: d, Provider: "jira"})
	})
}

func TestTicketsNoticeNoneWhenClean(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	if got, err := ticketsNoticeSource(t, nil)(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
	driftAndFail(t, f.repo.Dir)
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "none")
	if got, err := ticketsNoticeSource(t, nil)(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("provider none: %+v, %v", got, err)
	}
}

func TestTicketsNoticeLongMessageUnderCut(t *testing.T) {
	msg := ticketsNoticeMessage(tickets.Summary{Drifted: 1 << 62, Failed: 1 << 62})
	if n := len([]rune(msg)); n >= 500 {
		t.Errorf("message has %d characters", n)
	}
}

func TestTicketsNoticeCachesByFileStat(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	var queries int
	src := ticketsNoticeSource(t, &queries)
	if got, err := src(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("first: %+v, %v", got, err)
	}
	if got, err := src(context.Background()); err != nil || len(got) != 0 || queries != 1 {
		t.Fatalf("second: %+v, %v, queries %d", got, err, queries)
	}
	driftAndFail(t, f.repo.Dir)
	got, err := src(context.Background())
	if err != nil || len(got) != 1 || queries != 2 {
		t.Fatalf("after change: %+v, %v, queries %d", got, err, queries)
	}
}

func TestTicketsNoticeSourceError(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	d, err := db.Open(f.ctx, filepath.Join(f.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(f.ctx, "DROP TABLE tickets"); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if _, err := ticketsNoticeSource(t, nil)(context.Background()); err == nil {
		t.Error("no error")
	}
}

func TestMCPTicketsNoticeSentOncePerSession(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	driftAndFail(t, f.repo.Dir)
	cs := mcpSession(t)
	m, _ := mcpCall(t, cs, "status", nil).StructuredContent.(map[string]any)
	got := semanticNotices(t, m)
	if len(got) != 1 || got[0]["code"] != "tickets_need_attention" || !strings.HasPrefix(got[0]["message"].(string), "Jira tickets need attention: 1 differ") {
		t.Fatalf("first call notices = %v", got)
	}
	m, _ = mcpCall(t, cs, "status", nil).StructuredContent.(map[string]any)
	if got := semanticNotices(t, m); len(got) != 0 {
		t.Errorf("second call notices = %v", got)
	}
}

func TestMCPTicketsNoticeUsesNoNetwork(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	driftAndFail(t, f.repo.Dir)
	f.srv.ResetRequests()
	cs := mcpSession(t)
	m, _ := mcpCall(t, cs, "status", nil).StructuredContent.(map[string]any)
	if len(semanticNotices(t, m)) != 1 {
		t.Fatalf("notices = %v", m["notices"])
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Errorf("%d requests reached Jira", n)
	}
}
