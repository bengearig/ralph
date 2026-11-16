// Package identity works out who is doing the work in a checkout.
package identity

import (
	"context"
	"os/exec"
	"os/user"
	"strings"
)

const unknown = "unknown"

// Resolve returns the git identity effective in dir as "Name <email>", or the
// name or email alone if only one is set. With neither it falls back to the
// operating system user name, then to "unknown". It never returns "".
func Resolve(ctx context.Context, dir string) string {
	name := gitConfig(ctx, dir, "user.name")
	email := gitConfig(ctx, dir, "user.email")
	switch {
	case name != "" && email != "":
		return name + " <" + email + ">"
	case name != "":
		return name
	case email != "":
		return email
	}
	if u, err := user.Current(); err == nil {
		if n := strings.TrimSpace(u.Username); n != "" {
			return n
		}
	}
	return unknown
}

// gitConfig treats every failure, not only an unset key, as unset.
func gitConfig(ctx context.Context, dir, key string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
