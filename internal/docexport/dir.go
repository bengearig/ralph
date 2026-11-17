package docexport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const dirSetting = "documents.export.dir"

// ResolveDir returns the absolute export directory for the relative path rel
// under root. Symbolic links in the existing part of the path are resolved, and
// nothing is created. The directory must be strictly inside the resolved root
// and outside .git and .ralph.
func ResolveDir(root, rel string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%s: resolve project root %s: %w", dirSetting, root, err)
	}
	resolvedRoot, err = filepath.Abs(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("%s: resolve project root %s: %w", dirSetting, root, err)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("%s: %q must be a relative path inside the project", dirSetting, rel)
	}

	cur := resolvedRoot
	elems := strings.Split(clean, string(filepath.Separator))
	for i, elem := range elems {
		next := filepath.Join(cur, elem)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			cur = filepath.Join(append([]string{cur}, elems[i:]...)...)
			break
		}
		if err != nil {
			return "", fmt.Errorf("%s: inspect %s: %w", dirSetting, next, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("%s: resolve %s: %w", dirSetting, next, err)
			}
			if info, err = os.Stat(target); err != nil {
				return "", fmt.Errorf("%s: inspect %s: %w", dirSetting, target, err)
			}
			next = target
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s: %s exists and is not a directory", dirSetting, next)
		}
		cur = next
	}

	inside, err := filepath.Rel(resolvedRoot, cur)
	if err != nil || inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: %q resolves to %s, which is not inside the project", dirSetting, rel, cur)
	}
	if first := strings.Split(inside, string(filepath.Separator))[0]; first == ".git" || first == ".ralph" {
		return "", fmt.Errorf("%s: %q resolves to %s, inside %s", dirSetting, rel, cur, first)
	}
	return cur, nil
}
