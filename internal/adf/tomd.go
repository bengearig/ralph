package adf

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"unicode"
)

// ToMarkdown renders an ADF document as normalized Markdown. Nodes it does not
// know give the Markdown of their content.
func ToMarkdown(d Doc) string {
	return strings.TrimSpace(renderBlocks(d.Content))
}

func isInline(n Node) bool {
	switch n.Type {
	case "text", "hardBreak", "mention", "emoji", "status", "date", "inlineCard", "inlineExtension":
		return true
	}
	return n.Text != ""
}

func groupInline(nodes []Node) []Node {
	var out []Node
	var run []Node
	flush := func() {
		if len(run) > 0 {
			out = append(out, Node{Type: "paragraph", Content: run})
			run = nil
		}
	}
	for _, n := range nodes {
		if isInline(n) {
			run = append(run, n)
			continue
		}
		flush()
		out = append(out, n)
	}
	flush()
	return out
}

func renderBlocks(nodes []Node) string {
	var parts []string
	for _, n := range groupInline(nodes) {
		if s := renderBlock(n); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func renderBlock(n Node) string {
	switch n.Type {
	case "paragraph":
		return renderParagraph(n.Content)
	case "heading":
		level := min(max(intAttr(n.Attrs, "level", 1), 1), 6)
		text := strings.TrimSpace(renderInline(n.Content, " "))
		if text == "" {
			return ""
		}
		return strings.Repeat("#", level) + " " + text
	case "bulletList", "orderedList":
		return strings.Join(renderList(n, ""), "\n")
	case "codeBlock":
		return renderCode(n)
	case "blockquote":
		inner := renderBlocks(n.Content)
		if inner == "" {
			return ""
		}
		lines := strings.Split(inner, "\n")
		for i, l := range lines {
			if l == "" {
				lines[i] = ">"
			} else {
				lines[i] = "> " + l
			}
		}
		return strings.Join(lines, "\n")
	case "rule":
		return "---"
	case "blockCard", "embedCard":
		if u := strAttr(n.Attrs, "url"); u != "" {
			return u
		}
	case "media":
		return "[attachment]"
	}
	if n.Text != "" {
		return renderParagraph([]Node{n})
	}
	return renderBlocks(n.Content)
}

func renderCode(n Node) string {
	var sb strings.Builder
	for _, c := range n.Content {
		sb.WriteString(c.Text)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(sb.String(), "\r\n", "\n"), "\r", "\n")
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	lang := ""
	if f := strings.Fields(strAttr(n.Attrs, "language")); len(f) > 0 && !strings.Contains(f[0], "`") {
		lang = f[0]
	}
	if text == "" {
		return fence + lang + "\n" + fence
	}
	return fence + lang + "\n" + text + "\n" + fence
}

func longestRun(s string, ch rune) int {
	best, cur := 0, 0
	for _, r := range s {
		if r == ch {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}

func renderList(n Node, indent string) []string {
	start := intAttr(n.Attrs, "order", 1)
	var out []string
	for idx, item := range n.Content {
		marker := "- "
		if n.Type == "orderedList" {
			marker = strconv.Itoa(start+idx) + ". "
		}
		children := item.Content
		if item.Type != "listItem" {
			children = []Node{item}
		}
		markerDone := false
		for _, ch := range groupInline(children) {
			if ch.Type == "bulletList" || ch.Type == "orderedList" {
				out = append(out, renderList(ch, indent+"  ")...)
				continue
			}
			s := renderBlock(ch)
			if s == "" {
				continue
			}
			for i, l := range strings.Split(s, "\n") {
				switch {
				case !markerDone && i == 0:
					out = append(out, indent+marker+l)
					markerDone = true
				case l == "":
					out = append(out, "")
				default:
					out = append(out, indent+"  "+l)
				}
			}
		}
	}
	return out
}

func renderParagraph(nodes []Node) string {
	raw := strings.Split(strings.TrimSpace(renderInline(nodes, "  \n")), "\n")
	var lines []string
	for _, l := range raw {
		if t := strings.TrimFunc(l, unicode.IsSpace); t != "" {
			lines = append(lines, escapeLineStart(t))
		}
	}
	for i := 0; i < len(lines)-1; i++ {
		lines[i] += "  "
	}
	return strings.Join(lines, "\n")
}

func escapeLineStart(t string) string {
	switch c := t[0]; {
	case c == '#' || c == '>':
		return `\` + t
	case c == '-' || c == '+':
		if len(t) == 1 || t[1] == ' ' || t[1] == '\t' || t[1] == '-' {
			return `\` + t
		}
	case c == '~':
		if strings.HasPrefix(t, "~~~") {
			return `\` + t
		}
	case c >= '0' && c <= '9':
		k := 0
		for k < len(t) && t[k] >= '0' && t[k] <= '9' {
			k++
		}
		if k <= 9 && k+1 < len(t) && (t[k] == '.' || t[k] == ')') && (t[k+1] == ' ' || t[k+1] == '\t') {
			return t[:k] + `\` + t[k:]
		}
	}
	return t
}

type item struct {
	hard  bool
	raw   bool
	text  string
	marks map[string]string
}

func (it item) has(mark string) bool {
	_, ok := it.marks[mark]
	return ok
}

var oneLine = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

func inlineItems(nodes []Node) []item {
	var items []item
	add := func(it item) {
		if it.text != "" || it.hard {
			items = append(items, it)
		}
	}
	for _, n := range nodes {
		switch n.Type {
		case "hardBreak":
			add(item{hard: true, text: "\n"})
		case "inlineCard":
			add(item{raw: true, text: oneLine.Replace(strAttr(n.Attrs, "url"))})
		case "mention":
			add(item{text: "@" + strings.TrimPrefix(oneLine.Replace(strAttr(n.Attrs, "text")), "@")})
		case "emoji":
			t := strAttr(n.Attrs, "text")
			if t == "" {
				t = strAttr(n.Attrs, "shortName")
			}
			add(item{text: oneLine.Replace(t)})
		case "status":
			add(item{text: oneLine.Replace(strAttr(n.Attrs, "text"))})
		case "date":
			add(item{text: oneLine.Replace(strAttr(n.Attrs, "timestamp"))})
		default:
			if n.Text == "" {
				for _, it := range inlineItems(n.Content) {
					add(it)
				}
				continue
			}
			it := item{text: oneLine.Replace(n.Text)}
			for _, m := range n.Marks {
				if markRank(m.Type) < len(markOrder) {
					if it.marks == nil {
						it.marks = map[string]string{}
					}
					it.marks[m.Type] = strAttr(m.Attrs, "href")
				}
			}
			add(it)
		}
	}
	return items
}

// renderInline renders inline items and checks that the result parses back to
// the same text and marks. When it does not, as with ambiguous runs of
// asterisks, emphasis and then every mark is dropped so the text survives.
func renderInline(nodes []Node, hard string) string {
	var items []item
	prevHard := true
	for _, it := range inlineItems(nodes) {
		if it.hard && prevHard {
			continue
		}
		prevHard = it.hard
		items = append(items, it)
	}
	var s string
	for stage := 0; stage < 3; stage++ {
		its := stripMarks(items, stage)
		s = renderItems(its, renderOrder, hard)
		if stage == 2 || sameInline(s, its, hard) {
			break
		}
	}
	return s
}

func mergeItems(items []item) []item {
	var out []item
	for _, it := range items {
		if k := len(out) - 1; k >= 0 && !it.hard && !it.raw && !out[k].hard && !out[k].raw && maps.Equal(out[k].marks, it.marks) {
			out[k].text += it.text
			continue
		}
		out = append(out, it)
	}
	return out
}

func stripMarks(items []item, stage int) []item {
	if stage == 0 {
		return mergeItems(items)
	}
	out := make([]item, len(items))
	for i, it := range items {
		marks := map[string]string{}
		for k, v := range it.marks {
			if stage == 1 && k != "strong" && k != "em" {
				marks[k] = v
			}
		}
		it.marks = marks
		it.raw = it.raw && stage == 1
		out[i] = it
	}
	return mergeItems(out)
}

type flatRune struct {
	r   rune
	sig string
}

func markSig(marks map[string]string) string {
	var sb strings.Builder
	for _, m := range markOrder {
		if _, ok := marks[m]; ok {
			sb.WriteString(m + ";")
		}
	}
	return sb.String()
}

func flatten(sequence []flatRune, text string, marks map[string]string) []flatRune {
	if _, ok := marks["code"]; ok {
		delete(marks, "strong")
		delete(marks, "em")
	}
	if h, ok := marks["link"]; ok && !isLinkable(h) {
		delete(marks, "link")
	}
	for _, r := range text {
		m := marks
		if unicode.IsSpace(r) {
			m = map[string]string{}
			for k, v := range marks {
				if k != "strong" && k != "em" {
					m[k] = v
				}
			}
		}
		sequence = append(sequence, flatRune{r, markSig(m)})
	}
	return sequence
}

func sameInline(s string, items []item, hard string) bool {
	var want, got []flatRune
	for _, it := range items {
		marks := map[string]string{}
		for k, v := range it.marks {
			marks[k] = v
		}
		switch {
		case it.hard && hard == "  \n":
			want = append(want, flatRune{'\n', "hard"})
		case it.hard:
			want = flatten(want, hard, nil)
		default:
			want = flatten(want, it.text, marks)
		}
	}
	if hard == "  \n" {
		s = strings.ReplaceAll(s, hard, "\n")
	}
	for _, n := range parseInline(s) {
		switch n.Type {
		case "hardBreak":
			got = append(got, flatRune{'\n', "hard"})
		default:
			marks := map[string]string{}
			for _, m := range n.Marks {
				marks[m.Type], _ = m.Attrs["href"].(string)
			}
			got = flatten(got, n.Text, marks)
		}
	}
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

var renderOrder = []string{"link", "strong", "em", "code"}

// markRun is how many items from the start share mark with the same value.
func markRun(items []item, mark string) int {
	n := 0
	for n < len(items) && items[n].has(mark) && items[n].marks[mark] == items[0].marks[mark] {
		n++
	}
	return n
}

func without(marks []string, drop string) []string {
	var out []string
	for _, m := range marks {
		if m != drop {
			out = append(out, m)
		}
	}
	return out
}

// renderItems wraps the longest run of a shared mark first, so a mark that
// spans its neighbours is the outer one. Code is only ever innermost.
func renderItems(items []item, remaining []string, hard string) string {
	var sb strings.Builder
	for i := 0; i < len(items); {
		best, bestLen := "", 0
		for _, m := range remaining {
			if m == "code" && len(remaining) > 1 {
				continue
			}
			if n := markRun(items[i:], m); n > bestLen {
				best, bestLen = m, n
			}
		}
		if best == "" {
			j := i + 1
			for j < len(items) && !hasAny(items[j], remaining, len(remaining) > 1) {
				j++
			}
			if len(remaining) > 1 {
				sb.WriteString(renderItems(items[i:j], []string{"code"}, hard))
			} else {
				sb.WriteString(leaf(items[i:j], hard))
			}
			i = j
			continue
		}
		run := items[i : i+bestLen]
		if best == "code" {
			var t strings.Builder
			for _, it := range run {
				t.WriteString(it.text)
			}
			sb.WriteString(codeMarkdown(t.String()))
		} else {
			sb.WriteString(wrapMark(best, run[0].marks[best], renderItems(run, without(remaining, best), hard)))
		}
		i += bestLen
	}
	return sb.String()
}

func hasAny(it item, marks []string, skipCode bool) bool {
	for _, m := range marks {
		if m == "code" && skipCode {
			continue
		}
		if it.has(m) {
			return true
		}
	}
	return false
}

func leaf(items []item, hard string) string {
	var sb strings.Builder
	for _, it := range items {
		switch {
		case it.hard:
			sb.WriteString(hard)
		case it.raw:
			sb.WriteString(it.text)
		default:
			sb.WriteString(escapeText(it.text))
		}
	}
	return sb.String()
}

func wrapMark(mark, href, inner string) string {
	if mark == "link" {
		if href == "" {
			return inner
		}
		return "[" + inner + "](" + escapeHref(href) + ")"
	}
	marker := "**"
	if mark == "em" {
		marker = "*"
	}
	core := strings.TrimSpace(inner)
	if core == "" {
		return inner
	}
	lead := inner[:len(inner)-len(strings.TrimLeftFunc(inner, unicode.IsSpace))]
	trail := inner[len(strings.TrimRightFunc(inner, unicode.IsSpace)):]
	return lead + marker + core + marker + trail
}

func codeMarkdown(text string) string {
	fence := strings.Repeat("`", longestRun(text, '`')+1)
	pad := strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") ||
		(strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.TrimSpace(text) != "")
	if pad {
		text = " " + text + " "
	}
	return fence + text + fence
}

func escapeHref(h string) string {
	var sb strings.Builder
	for _, r := range h {
		switch {
		case r == '\\' || r == '(' || r == ')':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case unicode.IsSpace(r) || unicode.IsControl(r):
			for _, b := range []byte(string(r)) {
				fmt.Fprintf(&sb, "%%%02X", b)
			}
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func escapeText(s string) string {
	rs := []rune(s)
	var sb strings.Builder
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case '\\', '`', '*', '[', ']', '<':
			sb.WriteByte('\\')
		case '_':
			end := i
			for end < len(rs) && rs[end] == '_' {
				end++
			}
			if i > 0 && end < len(rs) && isWordRune(rs[i-1]) && isWordRune(rs[end]) {
				sb.WriteString(string(rs[i:end]))
				i = end - 1
				continue
			}
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func strAttr(attrs map[string]any, key string) string {
	switch v := attrs[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	}
	return ""
}

func intAttr(attrs map[string]any, key string, def int) int {
	switch v := attrs[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
