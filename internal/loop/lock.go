package loop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bengearig/ralph/internal/project"
)

var errLockHeld = errors.New("lock held")

// lock is an advisory exclusive lock on a file; the operating system drops it
// when the process ends.
type lock struct {
	f       *os.File
	path    string
	created bool
}

func lockPath(root string) string {
	return filepath.Join(project.RalphDir(root), project.LoopLockFile)
}

// acquireLock takes the loop lock without waiting; it returns errLockHeld when another holds it.
func acquireLock(root string) (*lock, error) {
	path := lockPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLockHeld) {
			return nil, errLockHeld
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &lock{f: f, path: path, created: errors.Is(statErr, os.ErrNotExist)}, nil
}

// release unlocks and closes the file; it is safe to call more than once.
func (l *lock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// discard releases the lock and removes the file when this process made it.
func (l *lock) discard() {
	if l == nil {
		return
	}
	l.release()
	if l.created {
		_ = os.Remove(l.path)
	}
}
