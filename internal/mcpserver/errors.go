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
	codeInternal       = "internal"
)

const notInitializedText = "ralph is not set up in this project: ask the user to run `ralph init` in the project directory, then try again."

// errorResult turns a Go error into a tool result with the error flag set and
// a structured error_code.
func errorResult(err error) *mcp.CallToolResult {
	code, text := classify(err)
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: map[string]any{"error_code": code, "message": text},
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
	}
	return codeInternal, "internal error: " + err.Error()
}
