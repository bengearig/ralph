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
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/followup"
	"github.com/spf13/cobra"
)

const minMonitorInterval = 10 * time.Second

var (
	defaultMonitorInterval = 60 * time.Second
	monitorNow             = time.Now
)

type monitorFlags struct {
	all, watch, noPoll, asJSON, apply bool
	interval                          time.Duration
}

func newMonitorCommand(a *app) *cobra.Command {
	var f monitorFlags
	cmd := &cobra.Command{
		Use:   "monitor [id]",
		Short: "Show the state of briefs and slices and poll the pull request host",
		Long: `Show every brief and slice that is not finished, with the pull request of
each slice and the follow-ups that apply would make. The host is asked about
open pull requests unless --no-poll is given. Nothing is changed: the view
writes no state, note, branch or lock, and it exits 0 when the host cannot be
used. With --watch --apply each round also applies the kinds named by the
follow_up.auto setting, as ralph apply does, before showing the view.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = strings.ToUpper(strings.TrimSpace(args[0]))
			}
			if f.apply && !f.watch {
				return errors.New("--apply needs --watch; use ralph apply for one run")
			}
			if !f.watch && cmd.Flags().Changed("interval") {
				return errors.New("--interval needs --watch")
			}
			if !cmd.Flags().Changed("interval") {
				f.interval = defaultMonitorInterval
			} else if f.interval < minMonitorInterval {
				return fmt.Errorf("--interval must be at least %s", minMonitorInterval)
			}
			return runMonitor(cmd, a, id, f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "include finished briefs and slices")
	cmd.Flags().BoolVar(&f.watch, "watch", false, "repeat until interrupted")
	cmd.Flags().DurationVar(&f.interval, "interval", defaultMonitorInterval, "time between rounds with --watch (at least 10s)")
	cmd.Flags().BoolVar(&f.apply, "apply", false, "with --watch, apply the follow-ups named by follow_up.auto each round")
	cmd.Flags().BoolVar(&f.noPoll, "no-poll", false, "show only the recorded state, without asking the host")
	cmd.Flags().BoolVar(&f.asJSON, "json", false, "print the view as JSON")
	return cmd
}

func runMonitor(cmd *cobra.Command, a *app, id string, f monitorFlags) error {
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
	out := cmd.OutOrStdout()
	opt := followup.ViewOptions{ID: id, All: f.all, Poll: !f.noPoll, Now: monitorNow}
	kinds := autoKinds(a)
	for {
		if f.apply {
			if err := applyRound(a.actorContext(ctx, cmd), out, fd, kinds); err != nil && ctx.Err() != nil {
				return nil
			}
		}
		view, err := followup.BuildView(ctx, fd, opt)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if f.asJSON {
			err = writeJSON(out, view)
		} else {
			err = writeMonitor(out, view)
		}
		if err != nil {
			return err
		}
		if !f.watch {
			return nil
		}
		_, _ = fmt.Fprintln(out)
		timer := time.NewTimer(f.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func applyRound(ctx context.Context, out io.Writer, fd followup.Deps, kinds []followup.Kind) error {
	res, err := followup.Apply(ctx, fd, followup.ApplyOptions{Kinds: kinds})
	if err != nil {
		_, _ = fmt.Fprintf(out, "apply failed: %v\n", err)
		return err
	}
	for _, l := range res.Lines {
		_, _ = fmt.Fprintln(out, l)
	}
	return nil
}

func writeMonitor(out io.Writer, v followup.View) error {
	var b strings.Builder
	w := func(format string, args ...any) { _, _ = fmt.Fprintf(&b, format, args...) }
	w("ralph monitor  %s  provider: %s\n", v.At.UTC().Format("2006-01-02 15:04:05 UTC"), v.Provider)
	const indent = "       "
	for _, br := range v.Briefs {
		w("%s  %s  %s\n", br.ID, br.Status, br.Title)
		for _, s := range br.Slices {
			w("  %s  %s  %s%s\n", s.ID, s.Status, s.Title, waitingSuffix(s.WaitingOn))
			if pr := s.PullRequest; pr != nil {
				state := pr.Recorded
				if pr.Host != "" && pr.Host != pr.Recorded {
					state = fmt.Sprintf("%s on the host (recorded: %s)", pr.Host, pr.Recorded)
				}
				if pr.Resolution != "" {
					state += ", " + pr.Resolution
				}
				draft := ""
				if pr.Draft {
					draft = " (draft)"
				}
				w("%spull request #%d %s%s %s\n", indent, pr.Number, state, draft, pr.URL)
				if s.NewFeedback > 0 {
					w("%sfeedback: %d new\n", indent, s.NewFeedback)
				}
				if s.Error != "" {
					w("%serror: %s\n", indent, s.Error)
				}
			}
			for _, f := range s.Followups {
				w("%snext: %s: %s\n", indent, f.Kind, f.Summary)
			}
		}
	}
	switch n := len(v.Followups); n {
	case 0:
		w("nothing to do\n")
	case 1:
		w("1 follow-up; ralph apply applies the ones named by the follow_up.auto setting or by flags\n")
	default:
		w("%d follow-ups; ralph apply applies the ones named by the follow_up.auto setting or by flags\n", n)
	}
	_, err := io.WriteString(out, b.String())
	return err
}
