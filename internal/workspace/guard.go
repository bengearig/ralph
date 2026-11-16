package workspace

import "context"

// GuardOptions names the base branch and the remote whose copy of it is compared.
type GuardOptions struct {
	Base, Remote string
}

// CheckStartable returns the state when quick work may start, and otherwise
// the first reason it may not.
func CheckStartable(ctx context.Context, w Workspace, o GuardOptions) (State, error) {
	st, err := w.State(ctx)
	if err != nil {
		return State{}, err
	}
	if st.Branch != o.Base {
		return State{}, &NotOnBaseError{Current: st.Branch, Base: o.Base}
	}
	if len(st.Changes) > 0 {
		return State{}, &DirtyError{Paths: st.Changes}
	}
	n, tracked, err := w.AheadOfRemote(ctx, o.Remote, o.Base)
	if err != nil {
		return State{}, err
	}
	if tracked && n > 0 {
		return State{}, &BaseAheadError{Remote: o.Remote, Base: o.Base, Count: n}
	}
	return st, nil
}
