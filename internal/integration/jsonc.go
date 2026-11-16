package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ParseJSONC returns standard JSON for a document that may hold comments,
// trailing commas and a leading byte order mark.
func ParseJSONC(doc []byte) ([]byte, error) {
	doc = bytes.TrimPrefix(doc, []byte("\xef\xbb\xbf"))
	stripped, err := stripComments(doc)
	if err != nil {
		return nil, err
	}
	return dropTrailingCommas(stripped), nil
}

func lineCol(doc []byte, off int) (line, col int) {
	prefix := doc[:off]
	line = bytes.Count(prefix, []byte("\n")) + 1
	col = off - bytes.LastIndexByte(prefix, '\n')
	return line, col
}

// skipString returns the index just past the string starting at doc[i], or -1
// when it is unterminated.
func skipString(doc []byte, i int) int {
	for j := i + 1; j < len(doc); j++ {
		switch doc[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

func stripComments(doc []byte) ([]byte, error) {
	out := make([]byte, 0, len(doc))
	for i := 0; i < len(doc); {
		c := doc[i]
		switch {
		case c == '"':
			end := skipString(doc, i)
			if end < 0 {
				line, col := lineCol(doc, i)
				return nil, fmt.Errorf("unterminated string at line %d, column %d", line, col)
			}
			out = append(out, doc[i:end]...)
			i = end
		case c == '/' && i+1 < len(doc) && doc[i+1] == '/':
			for i < len(doc) && doc[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(doc) && doc[i+1] == '*':
			end := bytes.Index(doc[i+2:], []byte("*/"))
			if end < 0 {
				line, col := lineCol(doc, i)
				return nil, fmt.Errorf("unterminated block comment at line %d, column %d", line, col)
			}
			i += end + 4
			out = append(out, ' ')
		default:
			out = append(out, c)
			i++
		}
	}
	return out, nil
}

// dropTrailingCommas works on comment-free text, so only whitespace can sit
// between a comma and its closing bracket.
func dropTrailingCommas(doc []byte) []byte {
	out := make([]byte, 0, len(doc))
	for i := 0; i < len(doc); {
		c := doc[i]
		switch c {
		case '"':
			end := skipString(doc, i)
			out = append(out, doc[i:end]...)
			i = end
		case ',':
			j := i + 1
			for j < len(doc) && (doc[j] == ' ' || doc[j] == '\t' || doc[j] == '\n' || doc[j] == '\r') {
				j++
			}
			if j < len(doc) && (doc[j] == '}' || doc[j] == ']') {
				i++
				continue
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

// GetPathJSONC is GetPath for a document that may hold comments.
func GetPathJSONC(doc []byte, keyPath []string) (json.RawMessage, bool, error) {
	std, err := ParseJSONC(doc)
	if err != nil {
		return nil, false, err
	}
	return GetPath(std, keyPath)
}

// HasArrayItemJSONC is HasArrayItem for a document that may hold comments.
func HasArrayItemJSONC(doc []byte, keyPath []string, item json.RawMessage) (bool, error) {
	std, err := ParseJSONC(doc)
	if err != nil {
		return false, err
	}
	return HasArrayItem(std, keyPath, item)
}
