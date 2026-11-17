// Package workspace is the seam between quick work and the way the code is
// checked out: the git operations it needs, behind one interface.
package workspace

import "context"

// State describes the checkout.
type State struct {
	// Branch is the short name of the checked-out branch.
	Branch string
	// Head is the full hash of the checked-out commit.
	Head string
	// Changes are the project-relative paths with modified, staged, deleted,
	// renamed or untracked files that git does not ignore, sorted, with the
	// configured ignored paths removed.
	Changes []string
}

// Commit is a commit with its full message.
type Commit struct {
	Hash, Message string
}

type Workspace interface {
	State(ctx context.Context) (State, error)
	BranchExists(ctx context.Context, name string) (bool, error)
	// CreateBranch creates the branch at HEAD and checks it out.
	CreateBranch(ctx context.Context, name string) error
	// CommitAll stages everything, commits with the message exactly and returns the new commit hash.
	CommitAll(ctx context.Context, message string) (string, error)
	// Push pushes the branch with upstream tracking and never forces.
	Push(ctx context.Context, remote, branch string) error
	// CommitsAhead counts commits reachable from HEAD and not from the local branch base.
	CommitsAhead(ctx context.Context, base string) (int, error)
	// Log lists the commits reachable from HEAD and not from the local branch
	// base, oldest first.
	Log(ctx context.Context, base string) ([]Commit, error)
	// AheadOfRemote counts commits on the local branch that its remote-tracking
	// branch lacks; tracked is false when that remote-tracking branch does not exist.
	AheadOfRemote(ctx context.Context, remote, branch string) (n int, tracked bool, err error)
	// BranchTip returns the commit a local branch points at; exists is false when there is no such branch.
	BranchTip(ctx context.Context, name string) (tip string, exists bool, err error)
	// DeleteBranch deletes a local branch whatever it holds; it refuses the checked-out branch.
	DeleteBranch(ctx context.Context, name string) error
	// RemoteBranchTip returns the commit a branch has on the remote, asking the remote itself.
	RemoteBranchTip(ctx context.Context, remote, branch string) (tip string, exists bool, err error)
	// DeleteRemoteBranch deletes a branch on the remote and never forces.
	DeleteRemoteBranch(ctx context.Context, remote, branch string) error
	// FastForward brings the checked-out branch up to the remote's branch of
	// the same name and fails when that is not a fast-forward; changed is
	// false when it was already up to date.
	FastForward(ctx context.Context, remote, branch string) (changed bool, err error)
}
