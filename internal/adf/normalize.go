package adf

import "strings"

const (
	truncationNote = "[truncated: the full text is in ralph]"
	minLimit       = 100
)

// Normalize rewrites Markdown into the form that survives a trip through ADF.
func Normalize(md string) string {
	return ToMarkdown(FromMarkdown(md))
}

// Limit cuts md to at most max characters, counting runes, at the last line
// break before the limit and appends a note that the full text lives in ralph.
func Limit(md string, max int) string {
	if max < minLimit {
		max = minLimit
	}
	rs := []rune(md)
	if len(rs) <= max {
		return md
	}
	note := "\n\n" + truncationNote
	avail := max - len([]rune(note))
	cut := rs[:avail]
	for i := avail; i > 0; i-- {
		if rs[i] == '\n' {
			cut = rs[:i]
			break
		}
	}
	head := strings.TrimRight(string(cut), " \t\n")
	if head == "" {
		return truncationNote
	}
	return head + note
}
