package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/tickets"
)

const ticketsNoticeCode = "tickets_need_attention"

// localTicketSummary summarizes the ticket rows. A failure is logged and reported as none, so it never fails
// the command.
func localTicketSummary(ctx context.Context, a *app, d *db.DB) *tickets.Summary {
	sum, err := tickets.Summarize(ctx, tickets.SummaryDeps{DB: d, Provider: a.setting("taskmanager.provider")})
	if err != nil {
		slog.Warn("cannot read the ticket state", "error", err)
		return nil
	}
	return &sum
}

// ticketsJSONValue is the tickets key of the status and monitor JSON, nil when nothing needs attention.
func ticketsJSONValue(s *tickets.Summary) *tickets.Summary {
	if s == nil || (s.Drifted == 0 && s.Failed == 0) {
		return nil
	}
	return s
}

// ticketsBlock is the text status and monitor print after their sections, or "" when nothing needs attention.
func ticketsBlock(s *tickets.Summary) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	if s.Drifted > 0 {
		fmt.Fprintf(&b, "\ntickets: %d with differences in Jira; run ralph tickets\n", s.Drifted)
	}
	if s.Failed > 0 {
		if s.Drifted == 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "tickets: %d failed to sync; run ralph tickets\n", s.Failed)
	}
	return b.String()
}

func ticketsNoticeMessage(s tickets.Summary) string {
	var parts []string
	if s.Drifted > 0 {
		parts = append(parts, fmt.Sprintf("%d differ from ralph's values (a person edited them in Jira)", s.Drifted))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed to sync", s.Failed))
	}
	return "Jira tickets need attention: " + strings.Join(parts, " and ") +
		". Ask the user how to proceed before running ralph tickets accept or overwrite; do not decide for them."
}

// ticketsNotice reports rows that differ in Jira or failed to sync. It reads local rows only and re-reads them
// only when the database file changes.
func ticketsNotice(a *app, store func(context.Context) (*db.DB, error)) mcpserver.NoticeSource {
	return ticketsNoticeWith(a, store, func(ctx context.Context, d *db.DB) (tickets.Summary, error) {
		return tickets.Summarize(ctx, tickets.SummaryDeps{DB: d, Provider: "jira"})
	})
}

func ticketsNoticeWith(a *app, store func(context.Context) (*db.DB, error), summarize func(context.Context, *db.DB) (tickets.Summary, error)) mcpserver.NoticeSource {
	var (
		mu      sync.Mutex
		have    bool
		key     [2]fileStat
		lastOut []mcpserver.Notice
	)
	return func(ctx context.Context) ([]mcpserver.Notice, error) {
		if a.setting("taskmanager.provider") != "jira" {
			return nil, nil
		}
		path := project.StorePath(a.root)
		if st, err := statFile(path); err != nil || !st.exists {
			return nil, err
		}
		// Opening the handle can create the write-ahead log, so it comes before the stat.
		d, err := store(ctx)
		if err != nil {
			return nil, err
		}
		var st [2]fileStat
		for i, p := range []string{path, path + "-wal"} {
			if st[i], err = statFile(p); err != nil {
				return nil, err
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if have && st == key {
			return lastOut, nil
		}
		sum, err := summarize(ctx, d)
		if err != nil {
			return nil, err
		}
		var out []mcpserver.Notice
		if sum.Drifted > 0 || sum.Failed > 0 {
			out = []mcpserver.Notice{{Code: ticketsNoticeCode, Message: ticketsNoticeMessage(sum)}}
		}
		have, key, lastOut = true, st, out
		return out, nil
	}
}
