package mcpserver

import (
	"errors"
	"os"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	codeNotInitialized = "not_initialized"
	codeNotFound       = "not_found"
	codeInvalid        = "invalid"
	codeInvalidParent  = "invalid_parent"
	codeTransition     = "invalid_transition"
	codeStale          = "stale"
	codeInternal       = "internal"
)

const notInitializedText = "ralph is not set up in this project: ask the user to run `ralph init` in the project directory, then try again."

// errorResult turns a Go error into a tool result with the error flag set and
// a structured error_code.
func errorResult(err error) *mcp.CallToolResult {
	code, text := classify(err)
	structured := map[string]any{"error_code": code, "message": text}
	var te *units.TransitionError
	if errors.As(err, &te) {
		allowed := make([]string, len(te.Allowed))
		for i, st := range te.Allowed {
			allowed[i] = string(st)
		}
		structured["allowed"] = allowed
	}
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: structured,
	}
}

func classify(err error) (code, text string) {
	var oe *openError
	switch {
	case errors.As(err, &oe):
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return codeNotInitialized, notInitializedText
		}
	case errors.Is(err, units.ErrNotFound):
		return codeNotFound, err.Error()
	case errors.Is(err, units.ErrInvalidParent):
		return codeInvalidParent, err.Error()
	case errors.Is(err, units.ErrInvalid):
		return codeInvalid, err.Error()
	case errors.Is(err, units.ErrInvalidTransition):
		return codeTransition, err.Error()
	case errors.Is(err, db.ErrStale):
		return codeStale, err.Error() + ". Call unit_get to read the unit's current fingerprint and state, then retry."
	}
	return codeInternal, "internal error: " + err.Error()
}
