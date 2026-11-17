// Package adf converts between a small Markdown subset and Atlassian Document
// Format, the JSON tree Jira Cloud uses for rich text fields.
package adf

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Doc is the root of an ADF document.
type Doc struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Content []Node `json:"content"`
}

// Node is one ADF node: a block, an inline node or a text run.
type Node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []Node         `json:"content,omitempty"`
	Text    string         `json:"text,omitempty"`
	Marks   []Mark         `json:"marks,omitempty"`
}

// Mark is a formatting mark on a text node.
type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// MarshalJSON always writes the documented root: version 1, type doc and a
// content list that is never null.
func (d Doc) MarshalJSON() ([]byte, error) {
	type plain Doc
	p := plain(d)
	p.Version = 1
	p.Type = "doc"
	if p.Content == nil {
		p.Content = []Node{}
	}
	return json.Marshal(p)
}

// ParseJSON decodes an ADF document.
func ParseJSON(raw []byte) (Doc, error) {
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return Doc{}, fmt.Errorf("decode ADF document: %w", err)
	}
	if d.Type != "doc" {
		return Doc{}, errors.New("decode ADF document: the root node is not a doc")
	}
	if d.Content == nil {
		d.Content = []Node{}
	}
	return d, nil
}
