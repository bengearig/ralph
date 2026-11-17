package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/docexport"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
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
	cmd.AddCommand(newDocsPurgeCommand(a), newDocsExportCommand(a))
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

type exportJSONFile struct {
	Path   string `json:"path"`
	ID     string `json:"id"`
	Action string `json:"action"`
}

type exportJSONConflict struct {
	Path string `json:"path"`
	ID   string `json:"id"`
}

type exportJSON struct {
	Dir       string               `json:"dir"`
	Files     []exportJSONFile     `json:"files"`
	Created   int                  `json:"created"`
	Updated   int                  `json:"updated"`
	Removed   int                  `json:"removed"`
	Unchanged int                  `json:"unchanged"`
	Conflicts []exportJSONConflict `json:"conflicts"`
}

func newDocsExportCommand(a *app) *cobra.Command {
	var (
		check  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the active documents to generated markdown files",
		Long: "Write one generated markdown file per active document into the directory of the setting documents.export.dir.\n" +
			"Files there that ralph generated are overwritten, and removed when their document is no longer exported.\n" +
			"Other files are never changed; a planned name taken by one is reported as a conflict and the command exits 1.\n" +
			"With --check nothing is written; the command exits 1 if an export would change anything or conflict.\n" +
			"Nothing is committed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			setting, ok := a.cfg.Get("documents.export.dir")
			if !ok {
				return errors.New("documents.export.dir is not set")
			}
			relDir := path.Clean(setting.Value)
			dir, err := docexport.ResolveDir(a.root, relDir)
			if err != nil {
				return err
			}
			plan, err := docexport.BuildPlan(ctx, newDocumentsService(a, d), units.NewService(d), dir)
			if err != nil {
				return err
			}

			var res docexport.Result
			if check {
				res = tallyPlan(plan)
			} else if res, err = plan.Apply(); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				doc := exportJSON{
					Dir: relDir, Files: []exportJSONFile{}, Conflicts: []exportJSONConflict{},
					Created: res.Created, Updated: res.Updated, Removed: res.Removed, Unchanged: res.Unchanged,
				}
				for _, c := range plan.Changes {
					p := path.Join(relDir, c.Name)
					doc.Files = append(doc.Files, exportJSONFile{Path: p, ID: c.ID, Action: string(c.Action)})
					if c.Action == docexport.ActionConflict {
						doc.Conflicts = append(doc.Conflicts, exportJSONConflict{Path: p, ID: c.ID})
					}
				}
				if err := writeJSON(out, doc); err != nil {
					return err
				}
			} else {
				writeExportText(out, relDir, plan, check)
				changes := res.Created + res.Updated + res.Removed
				switch {
				case !check:
					_, _ = fmt.Fprintf(out, "exported: created %d, updated %d, removed %d, unchanged %d\n", res.Created, res.Updated, res.Removed, res.Unchanged)
				case changes == 0 && len(res.Conflicts) == 0:
					_, _ = fmt.Fprintln(out, "up to date")
				}
			}

			switch changes := res.Created + res.Updated + res.Removed; {
			case len(res.Conflicts) > 0 && check:
				return &reportedError{errors.New("export would conflict")}
			case len(res.Conflicts) > 0:
				return &reportedError{errors.New("export left conflicts")}
			case check && changes > 0:
				return &reportedError{errors.New("export would change files")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing; exit 1 if the export would change a file or conflict")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one JSON object")
	return cmd
}

func tallyPlan(p docexport.Plan) docexport.Result {
	var res docexport.Result
	for _, c := range p.Changes {
		switch c.Action {
		case docexport.ActionCreated:
			res.Created++
		case docexport.ActionUpdated:
			res.Updated++
		case docexport.ActionRemoved:
			res.Removed++
		case docexport.ActionUnchanged:
			res.Unchanged++
		case docexport.ActionConflict:
			res.Conflicts = append(res.Conflicts, c)
		}
	}
	return res
}

func writeExportText(out io.Writer, relDir string, p docexport.Plan, check bool) {
	for _, c := range p.Changes {
		file := path.Join(relDir, c.Name)
		switch c.Action {
		case docexport.ActionConflict:
			_, _ = fmt.Fprintf(out, "conflict %s: %s\n", file, c.Detail)
		case docexport.ActionUnchanged:
		case docexport.ActionCreated, docexport.ActionUpdated, docexport.ActionRemoved:
			verb := string(c.Action)
			if check {
				verb = "would " + strings.TrimSuffix(verb, "d")
			}
			_, _ = fmt.Fprintf(out, "%s %s\n", verb, file)
		}
	}
}
