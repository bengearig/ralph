package loop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bengearig/ralph/internal/filelock"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
)

// BranchAction says what the loop does with the slice's branch.
type BranchAction string

const (
	// Create makes the branch from the base and checks it out.
	Create BranchAction = "create"
	// Resume continues on the branch the checkout is already on.
	Resume BranchAction = "resume"
)

// NotSliceError reports a unit that is not a slice.
type NotSliceError struct {
	ID    string
	Level units.Level
}

func (e *NotSliceError) Error() string {
	return fmt.Sprintf("%s is a %s, not a slice: the loop works one slice at a time (a task with no slice is quick work: ralph quick start)", e.ID, e.Level)
}

// WaitingError reports a slice that waits for unfinished slices.
type WaitingError struct {
	Slice   string
	Waiting []units.Dependency
}

func (e *WaitingError) Error() string {
	parts := make([]string, len(e.Waiting))
	for i, w := range e.Waiting {
		parts[i] = fmt.Sprintf("%s (%s)", w.ID, w.Title)
	}
	return fmt.Sprintf("%s waits for %s: finish and merge that first", e.Slice, strings.Join(parts, ", "))
}

// LockHeldError reports another loop holding the lock.
type LockHeldError struct{ Path string }

func (e *LockHeldError) Error() string {
	return fmt.Sprintf("another loop is running in this project (it holds %s): wait for it to end, or stop it, and try again", e.Path)
}

// NotIgnoredError reports ralph's local loop files that git does not ignore.
type NotIgnoredError struct{ Paths []string }

func (e *NotIgnoredError) Error() string {
	return fmt.Sprintf("git does not ignore %s: run \"ralph init\", commit the .ralph/.gitignore change, and try again",
		strings.Join(e.Paths, " and "))
}

// WrongBranchError reports a checkout on neither the base branch nor the slice's branch.
type WrongBranchError struct {
	Current, Base, Branch string
}

func (e *WrongBranchError) Error() string {
	return fmt.Sprintf("the current branch is %q, which is neither the base branch %q nor the slice's branch %q: run git switch %s to start the slice, or git switch %s to continue it",
		e.Current, e.Base, e.Branch, e.Base, e.Branch)
}

// BranchExistsError reports a slice branch that exists while the checkout is on the base.
type BranchExistsError struct{ Branch string }

func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("the slice's branch %s already exists: run git switch %s to continue the slice there", e.Branch, e.Branch)
}

// Stop is a task that ends the loop before it runs. Detail says why, led by the
// task's state; Reason names the task as well.
type Stop struct {
	Task   string
	State  units.State
	Detail string
	Reason string
}

// PrepareInput says which slice to prepare.
type PrepareInput struct {
	Slice string
	// DryRun takes no lock and changes nothing.
	DryRun bool
}

// Plan is what the loop will do.
type Plan struct {
	Slice units.Unit
	// Brief is the zero value when the slice has none.
	Brief  units.Unit
	Base   string
	Branch string
	Action BranchAction
	// Tasks are the slice's tasks in position order, as they are after recovery.
	Tasks []units.Unit
	// Runnable are the tasks to work, in order, up to any Stop.
	Runnable []units.Unit
	Stop     *Stop

	lock *filelock.Lock
}

// Close releases the lock; it is safe to call more than once and on a dry run.
func (p *Plan) Close() {
	if p != nil {
		p.lock.Release()
	}
}

// Prepare checks the guards in order and, unless it is a dry run, takes the lock,
// creates the slice's branch when needed and settles tasks an interrupted run left in progress.
func Prepare(ctx context.Context, d Deps, in PrepareInput) (plan Plan, err error) {
	slice, err := d.Units.Get(ctx, in.Slice)
	if err != nil {
		return Plan{}, err
	}
	if slice.Level != units.LevelSlice {
		return Plan{}, &NotSliceError{ID: slice.ID, Level: slice.Level}
	}
	if err := prstep.Decision(ctx, d.Units, slice.ID); err != nil {
		return Plan{}, err
	}
	waiting, err := d.Units.Waiting(ctx, slice.ID)
	if err != nil {
		return Plan{}, err
	}
	if len(waiting) > 0 {
		return Plan{}, &WaitingError{Slice: slice.ID, Waiting: waiting}
	}

	if !in.DryRun {
		l, err := filelock.Acquire(lockPath(d.Root))
		if errors.Is(err, filelock.ErrHeld) {
			return Plan{}, &LockHeldError{Path: lockPath(d.Root)}
		}
		if err != nil {
			return Plan{}, err
		}
		plan.lock = l
		defer func() {
			if err != nil {
				l.Release()
				plan = Plan{}
			}
		}()
		if err := checkIgnored(ctx, d.Root); err != nil {
			l.Discard()
			return Plan{}, err
		}
	} else if err := checkIgnored(ctx, d.Root); err != nil {
		return Plan{}, err
	}

	plan.Slice = slice
	if slice.ParentID != "" {
		if plan.Brief, err = d.Units.Get(ctx, slice.ParentID); err != nil {
			return plan, err
		}
	}
	remote := settingValue(d, "pr.remote")
	plan.Base = hosting.BaseBranch(ctx, d.Root, remote, settingValue(d, "pr.base_branch"))
	plan.Branch, err = prstep.SliceBranch(ctx, d.Units, settingValue(d, "git.branch_template"), slice)
	if err != nil {
		return plan, err
	}

	st, err := d.Workspace.State(ctx)
	if err != nil {
		return plan, err
	}
	switch st.Branch {
	case plan.Base:
		plan.Action = Create
	case plan.Branch:
		plan.Action = Resume
	default:
		return plan, &WrongBranchError{Current: st.Branch, Base: plan.Base, Branch: plan.Branch}
	}
	if len(st.Changes) > 0 {
		return plan, &workspace.DirtyError{Paths: st.Changes}
	}
	if plan.Action == Create {
		n, tracked, err := d.Workspace.AheadOfRemote(ctx, remote, plan.Base)
		if err != nil {
			return plan, err
		}
		if tracked && n > 0 {
			return plan, &workspace.BaseAheadError{Remote: remote, Base: plan.Base, Count: n}
		}
		exists, err := d.Workspace.BranchExists(ctx, plan.Branch)
		if err != nil {
			return plan, err
		}
		if exists {
			return plan, &BranchExistsError{Branch: plan.Branch}
		}
	}

	if !in.DryRun {
		if plan.Action == Create {
			if err := d.Workspace.CreateBranch(ctx, plan.Branch); err != nil {
				return plan, fmt.Errorf("create the branch %s: %w", plan.Branch, err)
			}
			if err := prstep.RecordBranch(ctx, d.Units, slice.ID, plan.Branch); err != nil {
				return plan, fmt.Errorf("record the branch %s: %w", plan.Branch, err)
			}
		}
		if err := recoverTasks(ctx, d, slice.ID, plan.Base); err != nil {
			return plan, err
		}
	}

	filter := slice.ID
	plan.Tasks, err = d.Units.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &filter})
	if err != nil {
		return plan, err
	}
	if err := plan.choose(ctx, d, in.DryRun); err != nil {
		return plan, err
	}
	return plan, nil
}

func settingValue(d Deps, path string) string {
	v, _ := d.Config.Get(path)
	return v.Value
}

// choose splits the tasks into those to run and the one that stops the loop.
func (p *Plan) choose(ctx context.Context, d Deps, dryRun bool) error {
	for _, t := range p.Tasks {
		switch t.State {
		case units.StateDone, units.StateCancelled:
		case units.StateTodo:
			p.Runnable = append(p.Runnable, t)
		case units.StateInProgress:
			if !dryRun {
				return fmt.Errorf("task %s is still in progress after recovery", t.ID)
			}
			p.Runnable = append(p.Runnable, t)
		default:
			detail, err := stopDetail(ctx, d, t)
			if err != nil {
				return err
			}
			p.Stop = &Stop{Task: t.ID, State: t.State, Detail: detail, Reason: t.ID + " is " + detail}
			return nil
		}
	}
	return nil
}

func stopDetail(ctx context.Context, d Deps, t units.Unit) (string, error) {
	if t.State != units.StateBlocked {
		return string(t.State) + ": it must be articulated and made TODO before the loop can run it", nil
	}
	notes, err := d.Units.Notes(ctx, t.ID)
	if err != nil {
		return "", err
	}
	reason := "no reason was recorded"
	for _, n := range notes {
		if n.Kind == units.NoteBlocked {
			reason = n.Body
			if _, after, ok := strings.Cut(n.Body, " to BLOCKED: "); ok {
				reason = after
			}
		}
	}
	return "BLOCKED: " + reason, nil
}

const recoveryReason = "loop restarted; the interrupted attempt left no commit"

// recoverTasks settles tasks left in progress by an interrupted run.
func recoverTasks(ctx context.Context, d Deps, sliceID, base string) error {
	parent := sliceID
	inProgress, err := d.Units.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &parent, States: []units.State{units.StateInProgress}})
	if err != nil || len(inProgress) == 0 {
		return err
	}
	commits, err := d.Workspace.Log(ctx, base)
	if err != nil {
		return fmt.Errorf("list the commits of the branch since %s: %w", base, err)
	}
	byTask := map[string]string{}
	for _, c := range commits {
		if id, ok := naming.ParseTaskTrailer(c.Message); ok {
			byTask[id] = c.Hash
		}
	}
	for _, t := range inProgress {
		hash, found := byTask[t.ID]
		if !found {
			if _, err := d.Units.SetState(ctx, t.ID, t.Fingerprint, units.StateTodo, recoveryReason); err != nil {
				return fmt.Errorf("return %s to TODO: %w", t.ID, err)
			}
			continue
		}
		if err := d.Units.AddLink(ctx, t.ID, units.LinkExternal, commitLinkPrefix+hash); err != nil {
			return err
		}
		if _, err := d.Units.AddNote(ctx, t.ID, units.NoteGeneral, "recovered: commit "+hash); err != nil {
			return err
		}
		if _, err := d.Units.SetState(ctx, t.ID, t.Fingerprint, units.StateDone, "recovered: commit "+short(hash)); err != nil {
			return fmt.Errorf("mark %s done: %w", t.ID, err)
		}
	}
	return nil
}

// checkIgnored refuses when git does not ignore the run records and the lock.
func checkIgnored(ctx context.Context, root string) error {
	var missing []string
	for _, rel := range []string{
		project.DirName + "/" + project.RunsDirName + "/",
		project.DirName + "/" + project.LoopLockFile,
	} {
		ignored, err := gitIgnores(ctx, root, rel)
		if err != nil {
			return err
		}
		if !ignored {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		return &NotIgnoredError{Paths: missing}
	}
	return nil
}

func gitIgnores(ctx context.Context, root, rel string) (bool, error) {
	probe := rel
	if strings.HasSuffix(rel, "/") {
		probe = rel + "x"
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", probe)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	case ctx.Err() != nil:
		return false, ctx.Err()
	}
	return false, fmt.Errorf("git check-ignore %s: %w", rel, err)
}

func lockPath(root string) string {
	return filepath.Join(project.RalphDir(root), project.LoopLockFile)
}
