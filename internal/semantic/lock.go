package semantic

import (
	"errors"
	"path/filepath"

	"github.com/bengearig/ralph/internal/filelock"
	"github.com/bengearig/ralph/internal/project"
)

// ErrIndexBusy means another process is writing the index.
var ErrIndexBusy = errors.New("another ralph index run is in progress")

// LockIndex takes the advisory lock of the index in dir without waiting. The lock is released by the returned
// function or when the process ends.
func LockIndex(dir string) (release func(), err error) {
	l, err := filelock.Acquire(filepath.Join(dir, project.IndexLockFile))
	if errors.Is(err, filelock.ErrHeld) {
		return nil, ErrIndexBusy
	}
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}
