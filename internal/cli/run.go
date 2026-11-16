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
		var reported *reportedError
		if errors.As(err, &reported) {
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "ralph: %v\n", err)
		return 1
	}
	return 0
}
