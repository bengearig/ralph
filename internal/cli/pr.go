package cli

import (
	"context"
	"errors"
	"fmt"
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
	cmd.AddCommand(newPRCreateCommand(a))
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
	ws, err := quick.NewLocalWorkspace(a.root)
	if err != nil {
		return err
	}
	settings := verify.FromConfig(a.cfg, a.root)
	draft, _ := strconv.ParseBool(a.setting("pr.draft"))
	out := cmd.OutOrStdout()
	deps := prstep.Deps{
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
	}
	res, err := prstep.Create(a.actorContext(ctx, cmd), deps, prstep.CreateInput{SliceID: sliceID})
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
