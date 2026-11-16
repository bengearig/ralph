package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/spf13/cobra"
)

const errPurgeCriteria = "say what to purge: give --keep N, --older-than DURATION or both"

var daysPattern = regexp.MustCompile(`^[0-9]+d$`)

// parseAge reads a Go duration or a whole number of days written like 90d.
// The age must be greater than zero.
func parseAge(s string) (time.Duration, error) {
	var d time.Duration
	if daysPattern.MatchString(s) {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil || n > int64(time.Duration(1<<63-1)/(24*time.Hour)) {
			return 0, fmt.Errorf("age %q is too large", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("age %q is not a duration such as 720h or a number of days such as 90d", s)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("age %q must be greater than zero", s)
	}
	return d, nil
}

func newDocsCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Maintain stored documents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newDocsPurgeCommand(a))
	return cmd
}

type purgeJSONEntry struct {
	ID       string `json:"id"`
	Versions []int  `json:"versions"`
	Bytes    int    `json:"bytes"`
}

type purgeJSON struct {
	DryRun    bool             `json:"dry_run"`
	Documents []purgeJSONEntry `json:"documents"`
	Versions  int              `json:"versions"`
	Bytes     int              `json:"bytes"`
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

func newDocsPurgeCommand(a *app) *cobra.Command {
	var (
		keep      int
		olderThan string
		document  string
		yes       bool
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Permanently delete earlier versions of documents; a dry run unless --yes is given",
		Long: "Permanently delete earlier versions of documents. A document's current content is never removed.\n" +
			"Without --yes nothing is removed: the command lists what it would remove.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			keepSet, olderSet := cmd.Flags().Changed("keep"), cmd.Flags().Changed("older-than")
			if !keepSet && !olderSet {
				return errors.New(errPurgeCriteria)
			}
			opts := documents.PurgeOptions{DocumentID: strings.ToUpper(document), Apply: yes}
			if keepSet {
				if keep < 0 {
					return fmt.Errorf("--keep %d: give a whole number of 0 or more", keep)
				}
				opts.Keep = &keep
			}
			if olderSet {
				age, err := parseAge(olderThan)
				if err != nil {
					return fmt.Errorf("--older-than: %w", err)
				}
				opts.OlderThan = age
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			d, err := openStore(ctx, a, db.Options{MustExist: true})
			if errors.Is(err, fs.ErrNotExist) {
				return errors.New(errNoDatabase)
			}
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()

			res, err := newDocumentsService(a, d).Purge(a.actorContext(ctx, cmd), opts)
			if err != nil {
				return err
			}
			if yes && res.Versions > 0 {
				if _, err := d.SQL().ExecContext(ctx, "VACUUM"); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ralph: warning: could not compact the database: %v\n", err)
				}
			}

			out := cmd.OutOrStdout()
			if asJSON {
				doc := purgeJSON{DryRun: !yes, Documents: []purgeJSONEntry{}, Versions: res.Versions, Bytes: res.Bytes}
				for _, e := range res.Entries {
					doc.Documents = append(doc.Documents, purgeJSONEntry{ID: e.ID, Versions: e.Versions, Bytes: e.Bytes})
				}
				return writeJSON(out, doc)
			}
			for _, e := range res.Entries {
				_, _ = fmt.Fprintf(out, "%s  versions %s  %d bytes\n", e.ID, joinInts(e.Versions), e.Bytes)
			}
			summary := fmt.Sprintf("versions %d, bytes %d, documents %d", res.Versions, res.Bytes, len(res.Entries))
			switch {
			case len(res.Entries) == 0:
				_, _ = fmt.Fprintln(out, "nothing to purge")
			case yes:
				_, _ = fmt.Fprintf(out, "removed: %s\n", summary)
			default:
				_, _ = fmt.Fprintf(out, "dry run: %s; nothing was removed; run again with --yes to remove them\n", summary)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 0, "protect the newest `N` earlier versions of each document")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "remove earlier versions made more than `DURATION` ago, such as 720h or 90d")
	cmd.Flags().StringVar(&document, "document", "", "only this document, such as D-3")
	cmd.Flags().BoolVar(&yes, "yes", false, "remove the versions instead of listing them")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one JSON object")
	return cmd
}
