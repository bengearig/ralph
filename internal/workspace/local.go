package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

var _ Workspace = (*Local)(nil)

// Local works in a checkout by running the git command.
type Local struct {
	root    string
	ignored []string
}

type Option func(*Local)

// WithIgnored excludes project-relative paths from State.Changes: an exact
// file path, or a directory path ending in "/" that covers everything below it.
func WithIgnored(paths []string) Option {
	return func(l *Local) { l.ignored = append(l.ignored, paths...) }
}

func NewLocal(root string, opts ...Option) *Local {
	l := &Local{root: root}
	for _, o := range opts {
		o(l)
	}
	return l
}

type result struct {
	stdout   string
	output   string
	exitCode int
}

// run reports a failed git as a result with a nonzero exit code, and any
// other failure (git missing, context ended) as an error.
func (l *Local) run(ctx context.Context, stdin string, args ...string) (result, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", l.root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), output: stdout.String() + stderr.String()}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			res.exitCode = exit.ExitCode()
			return res, nil
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, fmt.Errorf("run git %s: %w", args[0], err)
	}
	return res, nil
}

// ok runs git and turns a nonzero exit into a *GitError.
func (l *Local) ok(ctx context.Context, stdin string, args ...string) (result, error) {
	res, err := l.run(ctx, stdin, args...)
	if err != nil {
		return res, err
	}
	if res.exitCode != 0 {
		return res, newGitError(args, res.exitCode, res.output)
	}
	return res, nil
}

func (l *Local) State(ctx context.Context) (State, error) {
	res, err := l.run(ctx, "", "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return State{}, err
	}
	switch res.exitCode {
	case 0:
	case 1:
		return State{}, ErrDetachedHead
	default:
		return State{}, newGitError([]string{"symbolic-ref"}, res.exitCode, res.output)
	}
	branch := strings.TrimSpace(res.stdout)

	head, err := l.run(ctx, "", "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		return State{}, err
	}
	switch head.exitCode {
	case 0:
	case 1:
		return State{}, ErrNoCommits
	default:
		return State{}, newGitError([]string{"rev-parse"}, head.exitCode, head.output)
	}

	status, err := l.ok(ctx, "", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return State{}, err
	}
	return State{
		Branch:  branch,
		Head:    strings.TrimSpace(head.stdout),
		Changes: l.filter(parseStatus(status.stdout)),
	}, nil
}

// parseStatus reads porcelain v1 -z output: "XY path" entries, where a
// rename or copy is followed by one more entry holding the old path.
func parseStatus(out string) []string {
	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		x, y := e[0], e[1]
		paths = append(paths, e[3:])
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
		}
	}
	return paths
}

func (l *Local) filter(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var kept []string
	for _, p := range paths {
		if seen[p] || l.isIgnored(p) {
			continue
		}
		seen[p] = true
		kept = append(kept, p)
	}
	sort.Strings(kept)
	return kept
}

func (l *Local) isIgnored(p string) bool {
	for _, ig := range l.ignored {
		if ig == p || (strings.HasSuffix(ig, "/") && strings.HasPrefix(p, ig)) {
			return true
		}
	}
	return false
}

// refExists reports whether a fully qualified ref exists.
func (l *Local) refExists(ctx context.Context, ref string) (bool, error) {
	res, err := l.run(ctx, "", "show-ref", "--verify", "--quiet", ref)
	if err != nil {
		return false, err
	}
	switch res.exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, newGitError([]string{"show-ref"}, res.exitCode, res.output)
	}
}

func (l *Local) BranchExists(ctx context.Context, name string) (bool, error) {
	return l.refExists(ctx, "refs/heads/"+name)
}

func (l *Local) CreateBranch(ctx context.Context, name string) error {
	res, err := l.run(ctx, "", "check-ref-format", "--branch", name)
	if err != nil {
		return err
	}
	if res.exitCode != 0 {
		return fmt.Errorf("%w: %q", ErrInvalidBranch, name)
	}
	exists, err := l.BranchExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %q", ErrBranchExists, name)
	}
	_, err = l.ok(ctx, "", "switch", "--create", name)
	return err
}

func (l *Local) CommitAll(ctx context.Context, message string) (string, error) {
	add := []string{"add", "-A", "--", "."}
	for _, ig := range l.ignored {
		add = append(add, ":(top,literal,exclude)"+ig)
	}
	if _, err := l.ok(ctx, "", add...); err != nil {
		return "", err
	}
	res, err := l.run(ctx, "", "diff", "--cached", "--quiet")
	if err != nil {
		return "", err
	}
	switch res.exitCode {
	case 0:
		return "", ErrNothingToCommit
	case 1:
	default:
		return "", newGitError([]string{"diff"}, res.exitCode, res.output)
	}
	if _, err := l.ok(ctx, message, "commit", "--cleanup=verbatim", "-F", "-"); err != nil {
		return "", err
	}
	head, err := l.ok(ctx, "", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(head.stdout), nil
}

func (l *Local) Push(ctx context.Context, remote, branch string) error {
	_, err := l.ok(ctx, "", "push", "--set-upstream", remote, branch)
	return err
}

func (l *Local) count(ctx context.Context, args ...string) (int, error) {
	res, err := l.ok(ctx, "", append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(res.stdout))
	if err != nil {
		return 0, fmt.Errorf("read commit count %q: %w", strings.TrimSpace(res.stdout), err)
	}
	return n, nil
}

func (l *Local) CommitsAhead(ctx context.Context, base string) (int, error) {
	return l.count(ctx, "HEAD", "^refs/heads/"+base)
}

func (l *Local) AheadOfRemote(ctx context.Context, remote, branch string) (int, bool, error) {
	tracking := "refs/remotes/" + remote + "/" + branch
	tracked, err := l.refExists(ctx, tracking)
	if err != nil || !tracked {
		return 0, false, err
	}
	n, err := l.count(ctx, "refs/heads/"+branch, "^"+tracking)
	if err != nil {
		return 0, true, err
	}
	return n, true, nil
}
