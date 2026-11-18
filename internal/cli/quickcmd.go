package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/spf13/cobra"
)

const errNoDatabase = `this project has no ralph database: run "ralph init" first`

// reportedError is a failure whose message was already written to standard
// error; Run exits 1 without printing it again.
type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

func newQuickCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quick",
		Short: "Do a small piece of work end to end: start it, then finish it as a pull request",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newQuickStartCommand(a), newQuickFinishCommand(a))
	return cmd
}

func newQuickStartCommand(a *app) *cobra.Command {
	var body, bodyFile, criteria, criteriaFile, issue string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "start <title> | --issue <ref>",
		Short: "Create a task and its branch for a small piece of work",
		Long: "Create a task and its branch for a small piece of work.\n" +
			"With --issue the work starts from a ticket: the ticket is read first, the title and the body default to its title\n" +
			"and description, and the title argument may be left out. A ticket that cannot be read creates nothing.",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("issue") {
				return cobra.MaximumNArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			title := ""
			if len(args) == 1 {
				title = args[0]
			}
			bodyText, err := textOrFile(cmd, body, "body-file", bodyFile)
			if err != nil {
				return err
			}
			criteriaText, err := textOrFile(cmd, criteria, "criteria-file", criteriaFile)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			q, closeStore, err := openQuick(ctx, a)
			if err != nil {
				return err
			}
			defer closeStore()

			st, err := q.Start(a.actorContext(ctx, cmd), quick.StartInput{Title: title, Body: bodyText, AcceptanceCriteria: criteriaText, Issue: issue})
			if err != nil {
				return err
			}
			res := mcpserver.NewQuickStartResult(st)
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, res)
			}
			_, _ = fmt.Fprintf(out, "started %s: %s\n", res.Task.ID, res.Task.Title)
			_, _ = fmt.Fprintf(out, "branch: %s (base %s)\n", res.Branch, res.Base)
			if res.Issue != nil {
				_, _ = fmt.Fprintf(out, "issue: %s %s (%s)\n", res.Issue.Key, res.Issue.Title, res.Issue.Status)
			}
			printVerification(out, res.Verification.Commands, res.Verification.Source)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "what to do and why (default: the title)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the body from a file, or standard input for -")
	cmd.Flags().StringVar(&criteria, "criteria", "", "how a reviewer can tell the work is done")
	cmd.Flags().StringVar(&criteriaFile, "criteria-file", "", "read the criteria from a file, or standard input for -")
	cmd.Flags().StringVar(&issue, "issue", "", "start from a ticket: its key or address")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.MarkFlagsMutuallyExclusive("criteria", "criteria-file")
	return cmd
}

func newQuickFinishCommand(a *app) *cobra.Command {
	var summary, summaryFile string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "finish [id]",
		Short: "Verify, commit, push and open a pull request for quick work",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summaryText, err := textOrFile(cmd, summary, "summary-file", summaryFile)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			q, closeStore, err := openQuick(ctx, a)
			if err != nil {
				return err
			}
			defer closeStore()

			out := cmd.OutOrStdout()
			if !asJSON {
				settings := verify.FromConfig(a.cfg, a.root)
				printVerification(out, settings.Commands, settings.Source)
			}
			in := quick.FinishInput{Summary: summaryText}
			if len(args) == 1 {
				in.ID = args[0]
			}
			fin, err := q.Finish(a.actorContext(ctx, cmd), in)
			var ve *quick.VerificationError
			if errors.As(err, &ve) {
				writeVerificationFailure(cmd.ErrOrStderr(), ve.Report)
				return &reportedError{err}
			}
			if err != nil {
				return err
			}
			res := mcpserver.NewQuickFinishResult(fin)
			if asJSON {
				return writeJSON(out, res)
			}
			_, _ = fmt.Fprintf(out, "delivered %s\n", res.Task.ID)
			if res.Committed {
				_, _ = fmt.Fprintf(out, "commit: %s\n", res.Commit)
			} else {
				_, _ = fmt.Fprintf(out, "commit: already committed (%s)\n", res.Commit)
			}
			pr := "pull request: " + res.PullRequest.URL
			if res.PullRequest.Draft {
				pr += " (draft)"
			}
			if res.PullRequestReused {
				pr += " (already open)"
			}
			_, _ = fmt.Fprintln(out, pr)
			_, _ = fmt.Fprintln(out, "nothing has been merged: a person reviews the pull request")
			_, _ = fmt.Fprintf(out, "return to the base branch with: git switch %s\n", res.Base)
			return nil
		},
	}
	cmd.Flags().StringVar(&summary, "summary", "", "short account of what changed, for the pull request description")
	cmd.Flags().StringVar(&summaryFile, "summary-file", "", "read the summary from a file, or standard input for -")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.MarkFlagsMutuallyExclusive("summary", "summary-file")
	return cmd
}

// openQuick opens the project's database and builds the quick work service on
// it. The returned function closes the database.
func openQuick(ctx context.Context, a *app) (*quick.Service, func(), error) {
	d, err := openStore(ctx, a, db.Options{MustExist: true})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, errors.New(errNoDatabase)
	}
	if err != nil {
		return nil, nil, err
	}
	q, err := newQuickService(ctx, a, units.NewService(d), d)
	if err != nil {
		_ = d.Close()
		return nil, nil, err
	}
	return q, func() { _ = d.Close() }, nil
}

// textOrFile returns the text flag, or the contents of the file flag, where
// "-" means standard input.
func textOrFile(cmd *cobra.Command, text, fileFlag, file string) (string, error) {
	if !cmd.Flags().Changed(fileFlag) {
		return text, nil
	}
	var (
		b   []byte
		err error
	)
	if file == "-" {
		b, err = io.ReadAll(cmd.InOrStdin())
	} else {
		b, err = os.ReadFile(file)
	}
	if err != nil {
		return "", fmt.Errorf("--%s: %w", fileFlag, err)
	}
	return string(b), nil
}

func printVerification(w io.Writer, commands []string, source string) {
	if len(commands) == 0 {
		_, _ = fmt.Fprintln(w, "verification: none configured")
		return
	}
	_, _ = fmt.Fprintf(w, "verification (%s):\n", source)
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %s\n", c)
	}
}

func writeVerificationFailure(w io.Writer, report verify.Report) {
	res, ok := report.Failed()
	if !ok {
		_, _ = fmt.Fprintln(w, "verification failed")
		return
	}
	switch {
	case res.TimedOut:
		_, _ = fmt.Fprintf(w, "verification failed: %s (timed out)\n", res.Command)
	case res.StartErr != "":
		_, _ = fmt.Fprintf(w, "verification failed: %s (could not start: %s)\n", res.Command, res.StartErr)
	default:
		_, _ = fmt.Fprintf(w, "verification failed: %s (exit %d)\n", res.Command, res.ExitCode)
	}
	if tail := strings.TrimRight(res.Output, "\n"); tail != "" {
		_, _ = fmt.Fprintln(w, tail)
	}
}
