package mcpserver

import (
	"context"
	"fmt"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const issueGetText = `Read one ticket from the issue source and return it: key, url, title, type, status, parent (the key of the parent ticket, empty when none), body (Markdown), instruction (the text to work from: the system, key and title, then the body) and system (the issue source, such as jira). ref is a ticket key such as ABC-123 or the address of the ticket. This reads the ticket only: it writes nothing, in ralph or in the issue source. An unparseable ref is error_code "invalid"; an unknown ticket is "not_found"; "not_configured", "unauthorized", "rate_limited" and "unavailable" say what to ask the user to do.`

type issueGetInput struct {
	Ref string `json:"ref" jsonschema:"a ticket key such as ABC-123, or the address of the ticket"`
}

// IssueResult is a ticket as issue_get and ralph issue show report it.
type IssueResult struct {
	Key         string `json:"key"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Parent      string `json:"parent" jsonschema:"the key of the parent ticket; empty when none"`
	Body        string `json:"body" jsonschema:"the ticket's description as Markdown"`
	Instruction string `json:"instruction" jsonschema:"the text work on the ticket starts from"`
	System      string `json:"system" jsonschema:"the issue source the ticket is in, such as jira"`
}

// NewIssueResult describes i.
func NewIssueResult(i issuesource.Issue) IssueResult {
	return IssueResult{
		Key: i.Ref.Key, URL: i.URL, Title: i.Title, Type: i.Type, Status: i.Status, Parent: i.Parent,
		Body: i.Body, Instruction: issuesource.Instruction(i), System: i.Ref.System,
	}
}

// NotConfiguredError says the issue source is not set up, and what to set.
type NotConfiguredError struct{ Message string }

func (e *NotConfiguredError) Error() string { return e.Message }

func (s *server) issueSource(ctx context.Context) (issuesource.Source, error) {
	s.issueMu.Lock()
	defer s.issueMu.Unlock()
	if s.issues != nil {
		return s.issues, nil
	}
	src, err := s.opts.Issues(ctx)
	if err != nil {
		return nil, &openError{err}
	}
	s.issues = src
	return src, nil
}

func (s *server) addIssueTool(srv *mcp.Server) {
	if s.opts.Issues == nil {
		return
	}
	tool := &mcp.Tool{
		Name:         "issue_get",
		Description:  issueGetText,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  schemaFor[issueGetInput](nil),
		OutputSchema: outputFor[IssueResult](),
	}
	withNoticesSchema(tool.OutputSchema.(*jsonschema.Schema))
	s.noteTool(tool)
	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in issueGetInput) (*mcp.CallToolResult, any, error) {
		ctx = units.WithActor(ctx, s.opts.Actor)
		res, o := s.runIssueGet(ctx, in)
		return s.deliver(ctx, res, o)
	})
}

func (s *server) runIssueGet(ctx context.Context, in issueGetInput) (*mcp.CallToolResult, any) {
	src, err := s.issueSource(ctx)
	if err != nil {
		return errorResult(err), nil
	}
	ref, err := src.Parse(in.Ref)
	if err != nil {
		return errorResult(err), nil
	}
	issue, err := src.Get(ctx, ref)
	if err != nil {
		return errorResult(err), nil
	}
	out := NewIssueResult(issue)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: issueSummary(out)}}}, out
}

func issueSummary(r IssueResult) string {
	text := fmt.Sprintf("%s  %s  %s  %s\n%s", r.Key, r.Type, r.Status, r.Title, r.URL)
	if r.Parent != "" {
		text += "\nparent: " + r.Parent
	}
	return text + "\n\n" + r.Instruction
}
