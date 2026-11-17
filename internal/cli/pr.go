package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/spf13/cobra"
)

func newPRCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Open and follow the pull request of a finished slice",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPRCreateCommand(a), newPRFeedbackCommand(a), newPRCheckCommand(a))
	return cmd
}

func newPRCreateCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "create <slice>",
		Short: "Verify, push a slice's branch and open its pull request, or publish new commits to it",
		Long: `Push the branch of a finished slice and open its pull request. When one is
already open for the branch, the new commits are published to it. Nothing is
merged: a person reviews the pull request.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRCreate(cmd, a, strings.ToUpper(strings.TrimSpace(args[0])))
		},
	}
}

func runPRCreate(cmd *cobra.Command, a *app, sliceID string) error {
	if a.root == "" {
		return errNotInRepository
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
	deps, err := prDeps(cmd, a, d)
	if err != nil {
		return err
	}
	res, err := prstep.Create(a.actorContext(ctx, cmd), deps, prstep.CreateInput{SliceID: sliceID})
	out := cmd.OutOrStdout()
	var ve *prstep.VerificationError
	if errors.As(err, &ve) {
		writeVerificationFailure(cmd.ErrOrStderr(), verify.Report{Results: []verify.Result{ve.Result}})
		return &reportedError{err}
	}
	if err != nil {
		return err
	}
	line := "pull request: " + res.PullRequest.URL
	switch {
	case res.Reused:
		line += " (reused)"
	case res.PullRequest.Draft:
		line += " (draft)"
	}
	_, _ = fmt.Fprintln(out, line)
	_, _ = fmt.Fprintln(out, "nothing has been merged: a person reviews the pull request")
	return nil
}

func prDeps(cmd *cobra.Command, a *app, d *db.DB) (prstep.Deps, error) {
	ws, err := quick.NewLocalWorkspace(a.root)
	if err != nil {
		return prstep.Deps{}, err
	}
	settings := verify.FromConfig(a.cfg, a.root)
	draft, _ := strconv.ParseBool(a.setting("pr.draft"))
	out := cmd.OutOrStdout()
	return prstep.Deps{
		Units:     units.NewService(d),
		Workspace: ws,
		Provider:  func(ctx context.Context) (hosting.Provider, error) { return openProvider(ctx, a) },
		Verifier:  verify.Runner{Dir: a.root, Timeout: settings.Timeout},
		Config: prstep.Config{
			Root:           a.root,
			Remote:         a.setting("pr.remote"),
			Base:           a.setting("pr.base_branch"),
			BranchTemplate: a.setting("git.branch_template"),
			CommitTemplate: a.setting("git.commit_template"),
			Draft:          draft,
			Verify:         settings,
		},
		Report: func(line string) { _, _ = fmt.Fprintln(out, line) },
	}, nil
}

func newPRFeedbackCommand(a *app) *cobra.Command {
	var create, all, asJSON bool
	cmd := &cobra.Command{
		Use:   "feedback <slice>",
		Short: "Read the review feedback on a slice's pull request and turn it into tasks",
		Long: `Read the feedback on the latest pull request of a slice. Inline comments and
reviews that request changes are eligible to become tasks of the slice; with
--create the ones that have no task yet are created, ready for the loop. With
--all, general comments and plain reviews with text are eligible too. Nothing
is changed on the pull request.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRFeedback(cmd, a, strings.ToUpper(strings.TrimSpace(args[0])), prstep.FeedbackOptions{Create: create, All: all}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "create a task for each eligible item that has none")
	cmd.Flags().BoolVar(&all, "all", false, "also make general comments and plain reviews with text eligible")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func runPRFeedback(cmd *cobra.Command, a *app, sliceID string, opt prstep.FeedbackOptions, asJSON bool) error {
	if a.root == "" {
		return errNotInRepository
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
	deps, err := prDeps(cmd, a, d)
	if err != nil {
		return err
	}
	rep, err := prstep.Feedback(a.actorContext(ctx, cmd), deps, sliceID, opt)
	out := cmd.OutOrStdout()
	if err != nil {
		if created := createdIDs(rep); len(created) > 0 {
			_, _ = fmt.Fprintln(out, "created "+strings.Join(created, ", "))
		}
		return err
	}
	if asJSON {
		return writeJSON(out, feedbackJSON(rep))
	}
	writeFeedbackText(out, rep, opt)
	return nil
}

func createdIDs(rep prstep.FeedbackReport) []string {
	var ids []string
	for _, it := range rep.Items {
		if it.Created != "" {
			ids = append(ids, it.Created)
		}
	}
	return ids
}

type feedbackPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

type feedbackItemJSON struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Author   string `json:"author"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	State    string `json:"state"`
	URL      string `json:"url"`
	Eligible bool   `json:"eligible"`
	Handled  string `json:"handled"`
	Created  string `json:"created"`
}

type feedbackDoc struct {
	Slice       string             `json:"slice"`
	PullRequest feedbackPR         `json:"pull_request"`
	Items       []feedbackItemJSON `json:"items"`
}

func feedbackJSON(rep prstep.FeedbackReport) feedbackDoc {
	doc := feedbackDoc{
		Slice:       rep.Slice.ID,
		PullRequest: feedbackPR{Number: rep.PullRequest.Number, URL: rep.PullRequest.URL, State: string(rep.PullRequest.State)},
		Items:       []feedbackItemJSON{},
	}
	for _, it := range rep.Items {
		f := it.Feedback
		doc.Items = append(doc.Items, feedbackItemJSON{
			ID: f.ID, Kind: string(f.Kind), Author: f.Author, Path: f.Path, Line: f.Line, State: string(f.State),
			URL: f.URL, Eligible: it.Eligible, Handled: it.Handled, Created: it.Created,
		})
	}
	return doc
}

const feedbackSnippetMax = 60

func writeFeedbackText(out io.Writer, rep prstep.FeedbackReport, opt prstep.FeedbackOptions) {
	var fresh, handled int
	for _, it := range rep.Items {
		switch {
		case it.Handled != "":
			handled++
		case it.Eligible:
			fresh++
		}
	}
	_, _ = fmt.Fprintf(out, "%s pull request #%d (%s): %s\n", rep.Slice.ID, rep.PullRequest.Number, rep.PullRequest.State, rep.PullRequest.URL)
	_, _ = fmt.Fprintf(out, "feedback: %d new, %d already handled, %d not turned into tasks\n", fresh, handled, len(rep.Items)-fresh-handled)
	for _, it := range rep.Items {
		f := it.Feedback
		where := "-"
		if f.Path != "" {
			where = f.Path
			if f.Line != 0 {
				where += ":" + strconv.Itoa(f.Line)
			}
		}
		snippet := strings.Join(strings.Fields(f.Body), " ")
		if snippet == "" {
			snippet = "-"
		} else if r := []rune(snippet); len(r) > feedbackSnippetMax {
			snippet = string(r[:feedbackSnippetMax]) + "…"
		}
		var what string
		switch {
		case it.Handled != "":
			what = it.Handled
		case it.Created != "":
			what = it.Created
		case it.Eligible:
			what = "would create"
		default:
			what = "not a task"
		}
		_, _ = fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s\n", f.Kind, f.ID, f.Author, where, snippet, what)
	}
	if opt.Create {
		if created := createdIDs(rep); len(created) > 0 {
			_, _ = fmt.Fprintln(out, "created "+strings.Join(created, ", "))
		} else {
			_, _ = fmt.Fprintln(out, "no new tasks")
		}
	}
}

func newPRCheckCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "check <slice>",
		Short: "Read the state of a slice's latest pull request from the host and record what changed",
		Long: `Read the latest pull request of a slice from the host and record its state. A
pull request closed without merging stops the slice until you choose what to
do. The command reports what it found; it exits with status 0 whatever that is.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRCheck(cmd, a, strings.ToUpper(strings.TrimSpace(args[0])))
		},
	}
}

func runPRCheck(cmd *cobra.Command, a *app, sliceID string) error {
	if a.root == "" {
		return errNotInRepository
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
	deps, err := prDeps(cmd, a, d)
	if err != nil {
		return err
	}
	res, err := prstep.Check(a.actorContext(ctx, cmd), deps, sliceID)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	id := res.Slice.ID
	_, _ = fmt.Fprintf(out, "%s pull request #%d (%s): %s\n", id, res.Record.Number, res.PullRequest.State, res.PullRequest.URL)
	switch res.Change.Kind {
	case prstep.ChangeClosed:
		_, _ = fmt.Fprintf(out, "closed without merging: %s needs a decision\n", id)
		for _, c := range []string{"reopen", "rearticulate", "cancel"} {
			_, _ = fmt.Fprintf(out, "  ralph pr %s %s\n", c, id)
		}
	case prstep.ChangeMerged:
		_, _ = fmt.Fprintln(out, "merged")
	case prstep.ChangeReopenedOnHost:
		_, _ = fmt.Fprintln(out, "reopened on the host")
	default:
		_, _ = fmt.Fprintln(out, "unchanged")
	}
	return nil
}
