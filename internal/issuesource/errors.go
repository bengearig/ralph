package issuesource

import "github.com/bengearig/ralph/internal/taskmanager"

// The errors are the task manager's, so errors.Is works across both packages.
var (
	ErrNotFound     = taskmanager.ErrNotFound
	ErrUnauthorized = taskmanager.ErrUnauthorized
	ErrInvalid      = taskmanager.ErrInvalid
	ErrUnavailable  = taskmanager.ErrUnavailable
	ErrRateLimited  = taskmanager.ErrRateLimited
)
