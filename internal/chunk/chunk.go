// Package chunk splits a record's text into the pieces that are embedded for semantic search.
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxChunks is the most chunks a record has; later text is only found by keyword search.
	MaxChunks = 200

	excerptChars  = 240
	defaultTarget = 1200
	blockJoin     = "\n\n"
)

// Input is the record to split; Kind is carried for callers and does not change the split.
type Input struct {
	Kind  string
	Title string
	Text  string
}

// Chunk is one piece of a record.
type Chunk struct {
	Seq     int
	Embed   string
	Text    string
	Excerpt string
}

type block struct {
	text string
	path string
}

// Split cuts in.Text into chunks of at most targetChars characters where the text allows it.
func Split(in Input, targetChars int) []Chunk {
	if targetChars <= 0 {
		targetChars = defaultTarget
	}
	text := strings.ReplaceAll(strings.ReplaceAll(in.Text, "\r\n", "\n"), "\r", "\n")
	blocks := refine(blocksOf(text), targetChars)
	if len(blocks) == 0 {
		return []Chunk{{Seq: 0, Embed: in.Title}}
	}

	var out []Chunk
	var cur []string
	curRunes := 0
	curPath := ""
	flush := func() {
		if len(cur) == 0 {
			return
		}
		body := strings.Join(cur, blockJoin)
		out = append(out, Chunk{Seq: len(out), Embed: embedText(in.Title, curPath, body), Text: body, Excerpt: excerpt(body)})
		cur, curRunes = nil, 0
	}
	for _, b := range blocks {
		n := utf8.RuneCountInString(b.text)
		if len(cur) > 0 && curRunes+len(blockJoin)+n > targetChars {
			flush()
			if len(out) == MaxChunks {
				return out
			}
		}
		if len(cur) == 0 {
			curPath, curRunes = b.path, n
		} else {
			curRunes += len(blockJoin) + n
		}
		cur = append(cur, b.text)
	}
	flush()
	return out
}

func embedText(title, path, body string) string {
	var b strings.Builder
	b.WriteString(title + "\n")
	if path != "" {
		b.WriteString(path + "\n")
	}
	b.WriteString("\n" + body)
	return b.String()
}

func excerpt(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	r := []rune(s)
	if len(r) <= excerptChars {
		return s
	}
	cut := excerptChars
	if r[cut] != ' ' {
		if i := lastSpace(r[:cut]); i > 0 {
			cut = i
		}
	}
	return strings.TrimRight(string(r[:cut]), " ") + "…"
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

// blocksOf splits text into blocks: runs of lines between blank lines, with a fenced code block as one block
// and a heading line starting a block; each block carries the heading path in force at its start.
func blocksOf(text string) []block {
	var (
		out    []block
		lines  []string
		levels [6]string
		path   string
	)
	flush := func() {
		if len(lines) == 0 {
			return
		}
		t := strings.TrimRightFunc(strings.Join(lines, "\n"), unicode.IsSpace)
		out = append(out, block{text: t, path: path})
		lines = nil
	}
	all := strings.Split(text, "\n")
	for i := 0; i < len(all); i++ {
		line := all[i]
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if fence, n := fenceOpen(line); n > 0 {
			flush()
			lines = append(lines, line)
			for i++; i < len(all); i++ {
				lines = append(lines, all[i])
				if fenceClose(all[i], fence, n) {
					break
				}
			}
			flush()
			continue
		}
		if level, title := heading(line); level > 0 {
			flush()
			levels[level-1] = title
			for j := level; j < len(levels); j++ {
				levels[j] = ""
			}
			path = joinPath(levels)
		}
		lines = append(lines, line)
	}
	flush()
	return out
}

func joinPath(levels [6]string) string {
	var parts []string
	for _, l := range levels {
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " > ")
}

func heading(line string) (level int, title string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(line[n+1:])
}

func fenceOpen(line string) (rune, int) {
	if line == "" || (line[0] != '`' && line[0] != '~') {
		return 0, 0
	}
	c := line[0]
	n := 0
	for n < len(line) && line[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return rune(c), n
}

func fenceClose(line string, fence rune, n int) bool {
	t := strings.TrimRight(line, " \t")
	return len(t) >= n && strings.Trim(t, string(fence)) == ""
}

// refine splits every block over twice the target into pieces of at most the target.
func refine(blocks []block, target int) []block {
	var out []block
	for _, b := range blocks {
		if utf8.RuneCountInString(b.text) <= 2*target {
			out = append(out, b)
			continue
		}
		for _, p := range pieces(b.text, target) {
			out = append(out, block{text: p, path: b.path})
		}
	}
	return out
}

func pieces(text string, target int) []string {
	return pack(splitSentences(text), target, func(sentence string) []string {
		return pack(splitWords(sentence), target, func(word string) []string { return chopRunes(word, target) })
	})
}

// pack joins atoms greedily into pieces of at most target characters; an atom longer than that is cut by split.
func pack(atoms []string, target int, split func(string) []string) []string {
	var out []string
	cur := ""
	emit := func() {
		if t := strings.TrimSpace(cur); t != "" {
			out = append(out, t)
		}
		cur = ""
	}
	for _, a := range atoms {
		if utf8.RuneCountInString(strings.TrimSpace(a)) > target {
			emit()
			for _, p := range split(a) {
				if t := strings.TrimSpace(p); t != "" {
					out = append(out, t)
				}
			}
			continue
		}
		if utf8.RuneCountInString(strings.TrimSpace(cur+a)) > target {
			emit()
		}
		cur += a
	}
	emit()
	return out
}

func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\n':
		case s[i] == ' ' && i > 0 && (s[i-1] == '.' || s[i-1] == '?' || s[i-1] == '!'):
		default:
			continue
		}
		out = append(out, s[start:i+1])
		start = i + 1
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitWords(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if !sp && inSpace {
			out = append(out, s[start:i])
			start = i
		}
		inSpace = sp
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func chopRunes(word string, n int) []string {
	r := []rune(strings.TrimSpace(word))
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}
