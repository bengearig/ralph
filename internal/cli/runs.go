package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/loop"
	"github.com/spf13/cobra"
)

const errRunsPurgeCriteria = "give --older-than or --slice, so purge never guesses what to remove"

type runsPurgeRecord struct {
	Path      string `json:"path"`
	Slice     string `json:"slice"`
	Task      string `json:"task"`
	StartedAt string `json:"started_at"`
	Bytes     int64  `json:"bytes"`
}

type runsPurgeJSON struct {
	Records []runsPurgeRecord `json:"records"`
	Removed int               `json:"removed"`
	Bytes   int64             `json:"bytes"`
	DryRun  bool              `json:"dry_run"`
}

func newRunsCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Maintain the loop's run records",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newRunsPurgeCommand(a))
	return cmd
}

func newRunsPurgeCommand(a *app) *cobra.Command {
	var (
		olderThan string
		slice     string
		yes       bool
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Remove run records; a dry run unless --yes is given",
		Long: "Remove the loop's run records, which may hold code, command output and secrets.\n" +
			"Without --yes nothing is removed: the command lists what it would remove.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			olderSet, sliceSet := cmd.Flags().Changed("older-than"), cmd.Flags().Changed("slice")
			if !olderSet && !sliceSet {
				return errors.New(errRunsPurgeCriteria)
			}
			var sel loop.PurgeSelection
			if olderSet {
				age, err := parseAge(olderThan)
				if err != nil {
					return fmt.Errorf("--older-than: %w", err)
				}
				sel.OlderThan = age
			}
			if sliceSet {
				sel.Slice = strings.ToUpper(strings.TrimSpace(slice))
				if !unitIDPattern.MatchString(sel.Slice) || sel.Slice[0] != 'S' {
					return fmt.Errorf("--slice %q: give a slice ID such as S-1", slice)
				}
			}
			if a.root == "" {
				return errNotInRepository
			}
			res, err := loop.PurgeRecords(a.root, sel, !yes, time.Now())
			out := cmd.OutOrStdout()
			if asJSON {
				doc := runsPurgeJSON{Records: []runsPurgeRecord{}, Removed: res.Removed, Bytes: res.Bytes, DryRun: !yes}
				for _, r := range res.Records {
					doc.Records = append(doc.Records, runsPurgeRecord{
						Path: r.Path, Slice: r.Slice, Task: r.Task,
						StartedAt: r.Started.UTC().Format(time.RFC3339), Bytes: r.Bytes,
					})
				}
				if werr := writeJSON(out, doc); werr != nil {
					return werr
				}
				return err
			}
			if !yes {
				for _, r := range res.Records {
					_, _ = fmt.Fprintf(out, "%s  %s  %s\n", r.Path, formatSize(r.Bytes), r.Started.UTC().Format(time.DateOnly))
				}
			}
			switch {
			case len(res.Records) == 0:
				_, _ = fmt.Fprintln(out, "no run records match")
			case yes:
				_, _ = fmt.Fprintf(out, "removed %d records, %s\n", res.Removed, formatSize(res.Bytes))
			default:
				_, _ = fmt.Fprintf(out, "%d records, %s\n", len(res.Records), formatSize(res.Bytes))
				_, _ = fmt.Fprintln(out, "run again with --yes to remove them")
			}
			return err
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "", "remove records started more than `DURATION` ago, such as 720h or 90d")
	cmd.Flags().StringVar(&slice, "slice", "", "only the records of this slice, such as S-3")
	cmd.Flags().BoolVar(&yes, "yes", false, "remove the records instead of listing them")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one JSON object")
	return cmd
}

func formatSize(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
}
