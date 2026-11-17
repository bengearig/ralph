package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/spf13/cobra"
)

func newApplyCommand(a *app) *cobra.Command {
	var closeFlag, feedback, continueFlag, asJSON bool
	cmd := &cobra.Command{
		Use:   "apply [slice]",
		Short: "Record what the pull request host reports and apply follow-ups",
		Long: `Ask the host about the pull requests of slices, record the state it reports,
and apply the follow-ups of the kinds named by flags: --close deletes the
branch of a merged slice and updates the base branch, --feedback creates tasks
from review feedback, --continue runs the loop for the next slice of the brief
after a merge (at most one slice per run, and never one started this way
before). With no flags the kinds in the follow_up.auto setting
are applied, which is nothing by default. A pull request closed without
merging is only reported: choose ralph pr reopen, rearticulate or cancel.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slice []string
			if len(args) == 1 {
				slice = []string{strings.ToUpper(strings.TrimSpace(args[0]))}
			}
			var kinds []followup.Kind
			if closeFlag {
				kinds = append(kinds, followup.KindClose)
			}
			if feedback {
				kinds = append(kinds, followup.KindFeedback)
			}
			if continueFlag {
				kinds = append(kinds, followup.KindContinue)
			}
			if len(kinds) == 0 {
				kinds = autoKinds(a)
			}
			return runApply(cmd, a, followup.ApplyOptions{Slices: slice, Kinds: kinds}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&closeFlag, "close", false, "delete the branches of merged slices and update the base branch")
	cmd.Flags().BoolVar(&feedback, "feedback", false, "create tasks from review feedback")
	cmd.Flags().BoolVar(&continueFlag, "continue", false, "run the loop for the next slice of a brief after a merge")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func followupDeps(cmd *cobra.Command, a *app, d *db.DB) (followup.Deps, error) {
	deps, err := prDeps(cmd, a, d)
	if err != nil {
		return followup.Deps{}, err
	}
	deps.Config.Base = hosting.BaseBranch(cmd.Context(), deps.Config.Root, deps.Config.Remote, deps.Config.Base)
	return followup.Deps{
		PR: deps, Units: deps.Units, Workspace: deps.Workspace, Provider: deps.Provider,
		DeleteBranches: a.setting("follow_up.delete_branches"),
		Loop:           loopRunner(a, d, cmd.OutOrStdout()),
	}, nil
}

func autoKinds(a *app) []followup.Kind {
	items, _ := a.cfg.List("follow_up.auto")
	var kinds []followup.Kind
	for _, it := range items {
		if k := followup.Kind(it); !containsKind(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

func containsKind(kinds []followup.Kind, k followup.Kind) bool {
	for _, c := range kinds {
		if c == k {
			return true
		}
	}
	return false
}

func runApply(cmd *cobra.Command, a *app, opt followup.ApplyOptions, asJSON bool) error {
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
	fd, err := followupDeps(cmd, a, d)
	if err != nil {
		return err
	}
	res, err := followup.Apply(a.actorContext(ctx, cmd), fd, opt)
	if err != nil && res.LoopStatus == 0 {
		return err
	}
	out := cmd.OutOrStdout()
	if asJSON {
		err = writeJSON(out, res)
	} else {
		err = writeApply(out, res, len(opt.Kinds) == 0)
	}
	if err != nil {
		return err
	}
	if res.LoopStatus != 0 {
		return &exitStatusError{status: res.LoopStatus, err: fmt.Errorf("the loop ended with status %d", res.LoopStatus)}
	}
	if res.NothingObserved {
		return &reportedError{errors.New("nothing could be observed")}
	}
	return nil
}

func writeApply(out io.Writer, res followup.ApplyResult, noKinds bool) error {
	var b strings.Builder
	for _, l := range res.Lines {
		b.WriteString(l + "\n")
	}
	fmt.Fprintf(&b, "apply: recorded %d, created %d tasks, closed %d slices\n", res.Recorded, res.Created, res.Closed)
	if noKinds && res.NotApplied > 0 {
		b.WriteString("nothing was applied; name what to apply with --close, --feedback or --continue, or set follow_up.auto\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// loopRunner runs the loop for a slice as ralph loop does, with the harness,
// model and permissions from the settings, and prints what the loop prints
// indented under a line naming the slice.
func loopRunner(a *app, d *db.DB, out io.Writer) followup.LoopRunner {
	return func(ctx context.Context, sliceID string) (int, error) {
		_, _ = fmt.Fprintf(out, "starting the loop for %s\n", sliceID)
		deps, choice, err := buildLoopDeps(ctx, a, d, loopFlags{})
		if err != nil {
			return 1, err
		}
		err = executeLoop(ctx, &indentWriter{w: out}, deps, choice, sliceID, false)
		var exit *exitStatusError
		switch {
		case err == nil:
			return 0, nil
		case errors.As(err, &exit):
			return exit.status, nil
		}
		return 1, err
	}
}

// indentWriter writes each line indented by two spaces.
type indentWriter struct {
	w       io.Writer
	partial bool
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	for _, line := range strings.SplitAfter(string(p), "\n") {
		if line == "" {
			continue
		}
		if !iw.partial {
			if _, err := io.WriteString(iw.w, "  "); err != nil {
				return 0, err
			}
		}
		if _, err := io.WriteString(iw.w, line); err != nil {
			return 0, err
		}
		iw.partial = !strings.HasSuffix(line, "\n")
	}
	return len(p), nil
}
