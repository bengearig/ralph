package semantic

import (
	"context"
	"fmt"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/vecindex"
)

// States of the semantic index as Summarize reports them.
const (
	StateOff        = "off"
	StateRebuilding = "rebuilding"
	StateEmpty      = "empty"
	StateStale      = "stale"
	StateOk         = "ok"
)

// Summary is the state of the semantic index for a person to see.
type Summary struct {
	State       string
	Reasons     []string
	Done, Total int
}

// SummaryDeps is what Summarize reads. Dir holds index.db and index.rebuild.db.
type SummaryDeps struct {
	DB       *db.DB
	Dir      string
	Settings embed.Settings
}

// Summarize reports the state of the index from local files only: it calls no provider, takes no lock and writes
// nothing, and an index file that does not exist is not created.
func Summarize(ctx context.Context, d SummaryDeps) (Summary, error) {
	if d.Settings.Provider == "none" {
		return Summary{State: StateOff}, nil
	}
	rd := RebuildDeps{Dir: d.Dir}
	done, total, building, err := RebuildProgress(ctx, d.DB, d.Dir)
	if err != nil {
		return Summary{}, err
	}
	if building {
		return Summary{State: StateRebuilding, Done: done, Total: total}, nil
	}
	exists, err := fileExists(rd.indexPath())
	if err != nil {
		return Summary{}, err
	}
	if !exists {
		return Summary{State: StateEmpty}, nil
	}
	idx, err := vecindex.Open(rd.indexPath())
	if err != nil {
		return Summary{}, fmt.Errorf("read the semantic index: %w", err)
	}
	defer func() { _ = idx.Close() }()
	f, err := Inspect(ctx, idx, d.Settings)
	if err != nil {
		return Summary{}, fmt.Errorf("read the semantic index: %w", err)
	}
	switch {
	case f.Records == 0:
		return Summary{State: StateEmpty}, nil
	case f.Stale:
		return Summary{State: StateStale, Reasons: f.Reasons}, nil
	}
	return Summary{State: StateOk}, nil
}
