package cli

import (
	"errors"
	"fmt"
	"io"
)

func Run(args []string, stdout, stderr io.Writer) int {
	cmd := NewRootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.Execute(); err != nil {
		var exit *exitStatusError
		if errors.As(err, &exit) {
			return exit.status
		}
		var reported *reportedError
		if errors.As(err, &reported) {
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "ralph: %v\n", err)
		return 1
	}
	return 0
}

// exitStatusError ends a command with an exit status after it printed its own message.
type exitStatusError struct {
	status int
	err    error
}

func (e *exitStatusError) Error() string { return e.err.Error() }
func (e *exitStatusError) Unwrap() error { return e.err }
