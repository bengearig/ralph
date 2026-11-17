package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/semantic"
)

const (
	semanticNoticeCode   = "semantic_index_stale"
	semanticNoticePrefix = "Semantic search is off: the index was built with a different embedding configuration ("
	semanticNoticeSuffix = `). Rebuilding re-embeds every record and can take a while; ask the user whether to run "ralph index --rebuild --yes".`
	maxSemanticNotice    = 500
)

// semanticIndexJSON is the semantic_index key of the status and monitor JSON.
type semanticIndexJSON struct {
	State   string   `json:"state"`
	Reasons []string `json:"reasons,omitempty"`
	Done    *int     `json:"done,omitempty"`
	Total   *int     `json:"total,omitempty"`
}

// localSummary summarizes the semantic index from local state. A failure is logged and reported as none, so it
// never fails the command.
func localSummary(ctx context.Context, a *app, d *db.DB) *semantic.Summary {
	_, settings, err := embed.FromConfig(a.cfg)
	if err != nil {
		slog.Warn("cannot read the semantic index state", "error", err)
		return nil
	}
	sum, err := semantic.Summarize(ctx, semantic.SummaryDeps{DB: d, Dir: project.RalphDir(a.root), Settings: settings})
	if err != nil {
		slog.Warn("cannot read the semantic index state", "error", err)
		return nil
	}
	return &sum
}

func semanticJSON(s *semantic.Summary) *semanticIndexJSON {
	if s == nil || s.State == semantic.StateOff {
		return nil
	}
	v := &semanticIndexJSON{State: s.State, Reasons: s.Reasons}
	if s.State == semantic.StateRebuilding {
		v.Done, v.Total = &s.Done, &s.Total
	}
	return v
}

// semanticLine is the line status and monitor print, or "" when there is nothing to say.
func semanticLine(s *semantic.Summary) string {
	if s == nil {
		return ""
	}
	switch s.State {
	case semantic.StateStale:
		return fmt.Sprintf(`semantic index: stale (%s); semantic search is off; run "ralph index --rebuild"`, strings.Join(s.Reasons, "; "))
	case semantic.StateRebuilding:
		return fmt.Sprintf("semantic index: rebuilding (%d of %d records)", s.Done, s.Total)
	}
	return ""
}

type fileStat struct {
	exists bool
	size   int64
	mod    time.Time
}

func statFile(path string) (fileStat, error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fileStat{}, nil
	}
	if err != nil {
		return fileStat{}, err
	}
	return fileStat{exists: true, size: fi.Size(), mod: fi.ModTime()}, nil
}

func semanticNoticeMessage(reasons []string) string {
	joined := strings.Join(reasons, "; ")
	budget := maxSemanticNotice - len([]rune(semanticNoticePrefix)) - len([]rune(semanticNoticeSuffix))
	if r := []rune(joined); len(r) > budget {
		joined = string(r[:budget-1]) + "…"
	}
	return semanticNoticePrefix + joined + semanticNoticeSuffix
}

// semanticNotice reports a stale semantic index once per change of the index files; it reads local state only.
func semanticNotice(a *app, store func(context.Context) (*db.DB, error)) mcpserver.NoticeSource {
	var (
		mu      sync.Mutex
		have    bool
		keyIdx  fileStat
		keyReb  fileStat
		lastOut []mcpserver.Notice
	)
	dir := project.RalphDir(a.root)
	return func(ctx context.Context) ([]mcpserver.Notice, error) {
		idxStat, err := statFile(project.IndexPath(a.root))
		if err != nil {
			return nil, err
		}
		rebStat, err := statFile(filepath.Join(dir, project.IndexRebuildFile))
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if have && idxStat == keyIdx && rebStat == keyReb {
			return lastOut, nil
		}
		_, settings, err := embed.FromConfig(a.cfg)
		if err != nil {
			return nil, err
		}
		var d *db.DB
		if settings.Provider != "none" {
			if d, err = store(ctx); err != nil {
				return nil, err
			}
		}
		sum, err := semantic.Summarize(ctx, semantic.SummaryDeps{DB: d, Dir: dir, Settings: settings})
		if err != nil {
			return nil, err
		}
		var out []mcpserver.Notice
		if sum.State == semantic.StateStale {
			out = []mcpserver.Notice{{Code: semanticNoticeCode, Message: semanticNoticeMessage(sum.Reasons)}}
		}
		have, keyIdx, keyReb, lastOut = true, idxStat, rebStat, out
		return out, nil
	}
}
