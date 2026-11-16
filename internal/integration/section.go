// Package integration edits the files ralph shares with the user.
package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	BeginMarker = "<!-- ralph:begin -->"
	EndMarker   = "<!-- ralph:end -->"
)

// Section locates the marked block in a document. Start is the offset of the
// begin-marker line and End the offset just past the end-marker line's newline.
type Section struct {
	Found      bool
	Start, End int
	Content    string
}

type markerLine struct {
	line       int
	start, end int // offsets of the line and of the byte after its newline
}

func isMarker(line []byte, marker string) bool {
	line = bytes.TrimSuffix(line, []byte("\r"))
	return string(bytes.Trim(line, " ")) == marker
}

// FindSection finds the marked block, if any.
func FindSection(doc []byte) (Section, error) {
	sec, _, _, err := locate(doc)
	return sec, err
}

func locate(doc []byte) (Section, markerLine, markerLine, error) {
	var begins, ends []markerLine
	pos, n := 0, 0
	for pos < len(doc) {
		n++
		next := len(doc)
		textEnd := len(doc)
		if i := bytes.IndexByte(doc[pos:], '\n'); i >= 0 {
			textEnd = pos + i
			next = textEnd + 1
		}
		line := doc[pos:textEnd]
		switch {
		case isMarker(line, BeginMarker):
			begins = append(begins, markerLine{n, pos, next})
		case isMarker(line, EndMarker):
			ends = append(ends, markerLine{n, pos, next})
		}
		pos = next
	}

	switch {
	case len(begins) == 0 && len(ends) == 0:
		return Section{}, markerLine{}, markerLine{}, nil
	case len(begins) > 1:
		return Section{}, markerLine{}, markerLine{}, fmt.Errorf("line %d: more than one %s marker (first at line %d)", begins[1].line, BeginMarker, begins[0].line)
	case len(ends) > 1:
		return Section{}, markerLine{}, markerLine{}, fmt.Errorf("line %d: more than one %s marker (first at line %d)", ends[1].line, EndMarker, ends[0].line)
	case len(ends) == 0:
		return Section{}, markerLine{}, markerLine{}, fmt.Errorf("line %d: %s has no matching %s", begins[0].line, BeginMarker, EndMarker)
	case len(begins) == 0:
		return Section{}, markerLine{}, markerLine{}, fmt.Errorf("line %d: %s has no matching %s", ends[0].line, EndMarker, BeginMarker)
	case ends[0].line < begins[0].line:
		return Section{}, markerLine{}, markerLine{}, fmt.Errorf("line %d: %s comes before %s at line %d", ends[0].line, EndMarker, BeginMarker, begins[0].line)
	}

	b, e := begins[0], ends[0]
	return Section{
		Found:   true,
		Start:   b.start,
		End:     e.end,
		Content: bodyBetween(doc, b, e),
	}, b, e, nil
}

// bodyBetween is the text between the marker lines without the newline next to
// each marker.
func bodyBetween(doc []byte, b, e markerLine) string {
	body := doc[b.end:e.start]
	body = bytes.TrimSuffix(body, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))
	return string(body)
}

func normalize(content string) string {
	content = strings.ReplaceAll(content, "\r", "")
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// SectionHash is the hash of content after normalizing line endings,
// trailing whitespace and surrounding blank lines.
func SectionHash(content string) string {
	sum := sha256.Sum256([]byte(normalize(content)))
	return hex.EncodeToString(sum[:])
}

// ReplaceSection sets the marked block's content, appending the block when the
// document has none. Bytes outside the block are never changed.
func ReplaceSection(doc []byte, content string) ([]byte, error) {
	sec, b, e, err := locate(doc)
	if err != nil {
		return nil, err
	}
	nl := "\n"
	if i := bytes.IndexByte(doc, '\n'); i > 0 && doc[i-1] == '\r' {
		nl = "\r\n"
	}
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\n", nl)

	if !sec.Found {
		var out bytes.Buffer
		out.Write(doc)
		if len(doc) > 0 {
			if doc[len(doc)-1] != '\n' {
				out.WriteString(nl)
			}
			out.WriteString(nl)
		}
		out.WriteString(BeginMarker + nl)
		out.WriteString(content + nl)
		out.WriteString(EndMarker + nl)
		return out.Bytes(), nil
	}

	if SectionHash(sec.Content) == SectionHash(content) {
		return doc, nil
	}

	var out bytes.Buffer
	out.Write(doc[:b.end])
	if content != "" {
		out.WriteString(content + nl)
	}
	out.Write(doc[e.start:])
	return out.Bytes(), nil
}
