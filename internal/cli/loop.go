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

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/spf13/cobra"
)

const (
	exitStalled     = 2
	exitInterrupted = 130
	defaultModel    = "(harness default)"
)

// loopAdapters lists the adapters the loop can run; tests replace it.
var loopAdapters = all.Adapters

// loopExecute runs a headless request; nil means headless.Execute. Tests replace it.
var loopExecute func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error)

func newLoopCommand(a *app) *cobra.Command {
	var dryRun bool
	var harnessName, model, permissions string
	cmd := &cobra.Command{
		Use:   "loop <slice>",
		Short: "Work one slice's tasks with a coding agent, one verified commit per task",
		Long: `Work the tasks of one slice in order on the slice's own branch. Each task is
run by the agent, checked with the verification commands and committed. Nothing is
pushed. The loop stops when a task stalls or is not ready.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLoop(cmd, a, strings.ToUpper(strings.TrimSpace(args[0])), loopFlags{
				dryRun: dryRun, harness: harnessName, model: model, permissions: permissions,
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and change nothing")
	cmd.Flags().StringVar(&harnessName, "harness", "", "the harness to run (setting loop.harness)")
	cmd.Flags().StringVar(&model, "model", "", "the model to ask the harness for (setting loop.model)")
	cmd.Flags().StringVar(&permissions, "permissions", "", "restricted or auto (setting headless.permissions)")
	return cmd
}

type loopFlags struct {
	dryRun                      bool
	harness, model, permissions string
}

type loopChoice struct {
	adapter harness.Adapter
	version string
	model   string
	mode    harness.PermissionMode
}

func runLoop(cmd *cobra.Command, a *app, sliceID string, f loopFlags) error {
	if a.root == "" {
		return errNotInRepository
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx = a.actorContext(ctx, cmd)

	d, err := openStore(ctx, a, db.Options{MustExist: true})
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New(errNoDatabase)
	}
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	choice, err := chooseLoopSettings(ctx, a, f)
	if err != nil {
		return err
	}
	deps, err := newLoopDeps(a, d, choice)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	deps.Report = func(e loop.Event) { printLoopEvent(out, e) }

	plan, err := loop.Prepare(ctx, deps, loop.PrepareInput{Slice: sliceID, DryRun: f.dryRun})
	if err != nil {
		return err
	}
	defer plan.Close()

	printLoopHeader(out, plan, choice, deps.Verify.Commands)
	if f.dryRun {
		printLoopTasks(out, plan)
		return nil
	}
	sum, err := loop.Run(ctx, deps, loop.RunInput{Plan: plan})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintf(out, "%s interrupted: the tree has changes, and the next run refuses until they are committed or discarded\n", plan.Slice.ID)
			return &exitStatusError{status: exitInterrupted, err: err}
		}
		return err
	}
	return finishLoop(out, plan, sum)
}

func chooseLoopSettings(ctx context.Context, a *app, f loopFlags) (loopChoice, error) {
	var c loopChoice
	adapter, det, err := chooseHarness(ctx, a, f.harness)
	if err != nil {
		return c, err
	}
	c.adapter, c.version = adapter, det.Version

	c.model = f.model
	if c.model == "" {
		c.model = a.setting("loop.model")
	}
	if s, ok := config.DefaultRegistry().Lookup("loop.model"); ok && s.Validate != nil {
		if err := s.Validate(c.model); err != nil {
			return c, fmt.Errorf("model %q: %w", c.model, err)
		}
	}

	mode := f.permissions
	if mode == "" {
		mode = a.setting("headless.permissions")
	}
	c.mode = harness.PermissionMode(mode)
	if !c.mode.Valid() {
		return c, fmt.Errorf("permissions %q: must be restricted or auto", mode)
	}
	return c, nil
}

func chooseHarness(ctx context.Context, a *app, flag string) (harness.Adapter, harness.Detection, error) {
	adapters := loopAdapters()
	name := flag
	if name == "" {
		name = a.setting("loop.harness")
	}
	if name != "" {
		for _, ad := range adapters {
			if ad.Name() != name {
				continue
			}
			det := ad.Detect(ctx, a.root)
			if det.Unsupported != "" {
				return nil, det, fmt.Errorf("the harness %s is not supported: %s", name, det.Unsupported)
			}
			if !det.Found {
				return nil, det, fmt.Errorf("the harness %s was not found: install it, or choose another with --harness or loop.harness", name)
			}
			return ad, det, nil
		}
		names := make([]string, len(adapters))
		for i, ad := range adapters {
			names[i] = ad.Name()
		}
		return nil, harness.Detection{}, fmt.Errorf("unknown harness %q: choose one of %s", name, strings.Join(names, ", "))
	}

	var found []harness.Adapter
	var dets []harness.Detection
	for _, ad := range adapters {
		if det := ad.Detect(ctx, a.root); det.Found {
			found, dets = append(found, ad), append(dets, det)
		}
	}
	switch len(found) {
	case 0:
		return nil, harness.Detection{}, errors.New("no harness was found: install one, or name it with --harness or the setting loop.harness")
	case 1:
		if dets[0].Unsupported != "" {
			return nil, dets[0], fmt.Errorf("the harness %s is not supported: %s", found[0].Name(), dets[0].Unsupported)
		}
		return found[0], dets[0], nil
	}
	names := make([]string, len(found))
	for i, ad := range found {
		names[i] = ad.Name()
	}
	return nil, harness.Detection{}, fmt.Errorf("more than one harness was found (%s): choose one with --harness or the setting loop.harness", strings.Join(names, ", "))
}

func newLoopDeps(a *app, d *db.DB, c loopChoice) (loop.Deps, error) {
	ws, err := quick.NewLocalWorkspace(a.root)
	if err != nil {
		return loop.Deps{}, err
	}
	svc := units.NewService(d)
	docs := documents.NewService(d)
	settings := verify.FromConfig(a.cfg, a.root)
	return loop.Deps{
		Units:          svc,
		Workspace:      ws,
		Prompt:         loop.PromptDeps{Units: svc, Links: doclinks.New(svc, docs), Docs: docs},
		Config:         a.cfg,
		Root:           a.root,
		Adapter:        c.adapter,
		HarnessVersion: c.version,
		MCP:            harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
		Mode:           c.mode,
		Model:          c.model,
		CommitTemplate: a.setting("git.commit_template"),
		Verify:         settings,
		Verifier:       verify.Runner{Dir: a.root, Timeout: settings.Timeout},
		Execute:        loopExecute,
		Recorder:       loop.NewRecorder(loop.RecorderOptions{Root: a.root, Secrets: loop.SecretValues(a.cfg)}),
	}, nil
}

func printLoopHeader(w io.Writer, plan loop.Plan, c loopChoice, commands []string) {
	_, _ = fmt.Fprintf(w, "slice:    %s  %s\n", plan.Slice.ID, plan.Slice.Title)
	if plan.Brief.ID != "" {
		_, _ = fmt.Fprintf(w, "brief:    %s  %s\n", plan.Brief.ID, plan.Brief.Title)
	}
	how := "resuming"
	if plan.Action == loop.Create {
		how = "creating from " + plan.Base
	}
	_, _ = fmt.Fprintf(w, "branch:   %s (%s)\n", plan.Branch, how)
	model := c.model
	if model == "" {
		model = defaultModel
	}
	_, _ = fmt.Fprintf(w, "harness:  %s, model %s, permissions %s\n", c.adapter.Name(), model, c.mode)
	if len(commands) == 0 {
		_, _ = fmt.Fprintln(w, "verify:   none configured")
	}
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "verify:   %s\n", c)
	}
}

func printLoopTasks(w io.Writer, plan loop.Plan) {
	_, _ = fmt.Fprintln(w, "tasks:")
	for _, t := range plan.Tasks {
		_, _ = fmt.Fprintf(w, "  %s  %s  %s\n", t.ID, t.State, t.Title)
	}
	if plan.Stop != nil {
		_, _ = fmt.Fprintf(w, "would stop at %s: %s\n", plan.Stop.Task, plan.Stop.Detail)
	}
}

func printLoopEvent(w io.Writer, e loop.Event) {
	switch e.Kind {
	case loop.EventAttemptStarted:
		_, _ = fmt.Fprintf(w, "%s  attempt %d of %d\n", e.Task, e.Attempt, e.Max)
	case loop.EventAttemptFailed:
		_, _ = fmt.Fprintf(w, "%s  attempt %d of %d failed: %s\n", e.Task, e.Attempt, e.Max, e.Message)
	case loop.EventTaskDone:
		line := fmt.Sprintf("%s  done  commit %s  %s", e.Task, shortCommit(e.Commit), e.Duration.Round(time.Second))
		if e.CostUSD > 0 {
			line += fmt.Sprintf("  $%.2f", e.CostUSD)
		}
		_, _ = fmt.Fprintln(w, line)
	case loop.EventTaskStalled:
		_, _ = fmt.Fprintf(w, "%s  stalled: %s\n", e.Task, e.Message)
	case loop.EventInfo:
		_, _ = fmt.Fprintf(w, "info: %s\n", e.Message)
	case loop.EventWarning:
		_, _ = fmt.Fprintf(w, "warning: %s\n", e.Message)
	}
}

func shortCommit(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func finishLoop(w io.Writer, plan loop.Plan, sum loop.Summary) error {
	slice := plan.Slice.ID
	if sum.Stopped == "" {
		if sum.Done == 0 {
			_, _ = fmt.Fprintf(w, "%s has nothing left to do, branch %s\n", slice, sum.Branch)
			return nil
		}
		noun := "tasks"
		if sum.Done == 1 {
			noun = "task"
		}
		_, _ = fmt.Fprintf(w, "%s done: %d %s, branch %s\n", slice, sum.Done, noun, sum.Branch)
		return nil
	}
	_, _ = fmt.Fprintf(w, "%s stopped: %s\n", slice, sum.Stopped)
	if sum.Blocked > 0 {
		task, _, _ := strings.Cut(sum.Stopped, " ")
		_, _ = fmt.Fprintf(w, "the tree was left as the agent left it; look at the run records under .ralph/%s/%s/\n", project.RunsDirName, slice)
		_, _ = fmt.Fprintf(w, "fix or revert it, unblock %s with a reason, and run the loop again\n", task)
	}
	return &exitStatusError{status: exitStalled, err: errors.New(sum.Stopped)}
}
