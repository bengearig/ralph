package naming

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// RenderBranch renders a branch name template and checks the result.
func RenderBranch(tmpl string, d Data) (string, error) {
	name, err := render(tmpl, d)
	if err != nil {
		return "", err
	}
	if err := ValidBranchName(name); err != nil {
		return "", fmt.Errorf("template renders %q: %w", name, err)
	}
	return name, nil
}

// ValidBranchName applies git's reference name rules to a branch name.
func ValidBranchName(name string) error {
	switch {
	case name == "":
		return errors.New("branch name is empty")
	case name == "@":
		return errors.New("branch name must not be @")
	case strings.HasPrefix(name, "-"):
		return errors.New("branch name must not start with a hyphen")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return errors.New("branch name must not start or end with a slash")
	case strings.HasSuffix(name, "."):
		return errors.New("branch name must not end with a dot")
	}
	for _, bad := range []string{"..", "@{", "//"} {
		if strings.Contains(name, bad) {
			return fmt.Errorf("branch name must not contain %q", bad)
		}
	}
	if i := strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("~^:?*[\\", r)
	}); i >= 0 {
		return errors.New("branch name contains a space, control character or one of ~ ^ : ? * [ \\")
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return errors.New("branch name component must not start with a dot")
		}
		if strings.HasSuffix(part, ".lock") {
			return errors.New("branch name component must not end with .lock")
		}
	}
	return nil
}
