package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	noticesField     = "notices"
	maxNoticeMessage = 500
	noticePrefix     = "Notice for the user: "
)

// Notice is a short message an agent should relay to the user.
type Notice struct {
	Code    string `json:"code" jsonschema:"stable identifier of the kind of notice"`
	Message string `json:"message" jsonschema:"what to tell the user"`
}

// NoticeSource reports the notices that currently apply. Sources run after
// every tool call and may be called concurrently.
type NoticeSource func(ctx context.Context) ([]Notice, error)

func noticesSchema() *jsonschema.Schema {
	item, err := jsonschema.For[Notice](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: notice schema: %v", err))
	}
	return &jsonschema.Schema{
		Type:        "array",
		Items:       item,
		Description: "messages the agent should relay to the user; each is sent once per session",
	}
}

// withNoticesSchema adds the notices property to an output schema.
func withNoticesSchema(sc *jsonschema.Schema) {
	if sc == nil {
		return
	}
	if sc.Properties == nil {
		sc.Properties = map[string]*jsonschema.Schema{}
	}
	sc.Properties[noticesField] = noticesSchema()
	for _, r := range sc.Required {
		if r == noticesField {
			return
		}
	}
	sc.Required = append(sc.Required, noticesField)
}

func truncateNotice(msg string) string {
	r := []rune(msg)
	if len(r) <= maxNoticeMessage {
		return msg
	}
	return string(r[:maxNoticeMessage-1]) + "…"
}

// collectNotices runs every source in order and returns the notices not yet
// attached in this session. A failing or panicking source is logged and
// skipped.
func (s *server) collectNotices(ctx context.Context) []Notice {
	var fresh []Notice
	s.mu.Lock()
	srcs := append(slices.Clone(s.opts.Sources), s.unitSources...)
	s.mu.Unlock()
	for i, src := range srcs {
		if src == nil {
			continue
		}
		got, ok := runSource(ctx, i, src)
		if !ok {
			continue
		}
		s.noticeMu.Lock()
		for _, n := range got {
			n.Message = truncateNotice(n.Message)
			if _, dup := s.seen[n]; dup {
				continue
			}
			if s.seen == nil {
				s.seen = map[Notice]struct{}{}
			}
			s.seen[n] = struct{}{}
			fresh = append(fresh, n)
		}
		s.noticeMu.Unlock()
	}
	return fresh
}

func runSource(ctx context.Context, index int, src NoticeSource) (got []Notice, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("notice source panicked", "source", index, "panic", fmt.Sprint(r))
			got, ok = nil, false
		}
	}()
	got, err := src(ctx)
	if err != nil {
		slog.Warn("notice source failed", "source", index, "error", err)
		return nil, false
	}
	return got, true
}

// attachNotices adds the notices to the result's structured content and
// appends a text item for each.
func attachNotices(res *mcp.CallToolResult, out any, fresh []Notice) (any, error) {
	if res.IsError {
		m, _ := res.StructuredContent.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m[noticesField] = noticeList(fresh)
		res.StructuredContent = m
	} else {
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("structured output is not an object: %w", err)
		}
		list, err := json.Marshal(noticeList(fresh))
		if err != nil {
			return nil, err
		}
		fields[noticesField] = list
		out = fields
	}
	for _, n := range fresh {
		res.Content = append(res.Content, &mcp.TextContent{Text: noticePrefix + n.Message})
	}
	return out, nil
}

func noticeList(ns []Notice) []Notice {
	if ns == nil {
		return []Notice{}
	}
	return ns
}
