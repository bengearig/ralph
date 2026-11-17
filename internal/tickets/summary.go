package tickets

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/units"
)

// Summary kinds.
const (
	SummaryDrift   = "drift"
	SummaryFailure = "failure"
)

// SummaryDeps are what Summarize reads.
type SummaryDeps struct {
	DB       *db.DB
	Provider string
}

// SummaryUnit is one unit whose ticket needs attention.
type SummaryUnit struct {
	Unit   string `json:"unit"`
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Summary counts the rows with an unresolved difference in Jira and the rows whose last push failed.
type Summary struct {
	Drifted int           `json:"drifted"`
	Failed  int           `json:"failed"`
	Units   []SummaryUnit `json:"units"`
}

// Summarize reads the ticket rows only: it never writes and never calls the task manager. It is empty unless
// the provider is jira.
func Summarize(ctx context.Context, d SummaryDeps) (Summary, error) {
	var sum Summary
	if d.Provider != "jira" {
		return sum, nil
	}
	rows, err := NewStore(d.DB).List(ctx)
	if err != nil {
		return Summary{}, err
	}
	svc := units.NewService(d.DB)
	for _, r := range rows {
		if r.Drift != "" {
			sum.Drifted++
			sum.Units = append(sum.Units, SummaryUnit{Unit: r.UnitID, Key: r.Key, Kind: SummaryDrift, Detail: strings.Join(strings.Split(r.Drift, ","), ", ")})
		}
		if r.LastError == "" {
			continue
		}
		if r.LinkConfirmed {
			links, err := svc.Links(ctx, r.UnitID)
			if err != nil {
				return Summary{}, fmt.Errorf("tickets: summarize %s: %w", r.UnitID, err)
			}
			if LinkKey(links) == "" {
				continue
			}
		}
		sum.Failed++
		sum.Units = append(sum.Units, SummaryUnit{Unit: r.UnitID, Key: r.Key, Kind: SummaryFailure, Detail: r.FailedStep + ": " + r.LastError})
	}
	return sum, nil
}
