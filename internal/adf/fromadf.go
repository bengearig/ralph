package adf

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const maxListDepth = 4

var (
	headingRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(\S.*)$`)
	ruleRe    = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	quoteRe   = regexp.MustCompile(`^ {0,3}>[ ]?(.*)$`)
	itemRe    = regexp.MustCompile(`^( *)([-*+]|\d{1,9}[.)])[ ]+(\S.*)$`)
	fenceRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
)

// FromMarkdown converts the supported Markdown subset to ADF. It never fails;
// anything it does not recognise becomes paragraph text.
func FromMarkdown(md string) Doc {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")
	blocks := parseBlocks(strings.Split(md, "\n"))
	if blocks == nil {
		blocks = []Node{}
	}
	return Doc{Version: 1, Type: "doc", Content: blocks}
}

func expandTabs(line string) string {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	if !strings.Contains(line[:n], "\t") {
		return line
	}
	return strings.ReplaceAll(line[:n], "\t", "    ") + line[n:]
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func openFence(line string) (ch byte, n int, lang string, ok bool) {
	m := fenceRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, "", false
	}
	if m[1][0] == '`' && strings.Contains(m[2], "`") {
		return 0, 0, "", false
	}
	if f := strings.Fields(m[2]); len(f) > 0 {
		lang = f[0]
	}
	return m[1][0], len(m[1]), lang, true
}

func closesFence(line string, ch byte, n int) bool {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return false
	}
	run := 0
	for run < len(t) && t[run] == ch {
		run++
	}
	return run >= n && strings.TrimSpace(t[run:]) == ""
}

func isItem(e string) bool { return itemRe.MatchString(e) && !ruleRe.MatchString(e) }

func startsBlock(e string) bool {
	if _, _, _, ok := openFence(e); ok {
		return true
	}
	return headingRe.MatchString(e) || ruleRe.MatchString(e) || quoteRe.MatchString(e) ||
		isItem(e) || strings.HasPrefix(strings.TrimSpace(e), "|")
}

func parseBlocks(lines []string) []Node {
	var out []Node
	for i := 0; i < len(lines); {
		e := expandTabs(lines[i])
		if isBlank(e) {
			i++
			continue
		}
		if ch, n, lang, ok := openFence(e); ok {
			var code []string
			for i++; i < len(lines) && !closesFence(expandTabs(lines[i]), ch, n); i++ {
				code = append(code, lines[i])
			}
			i++
			out = append(out, codeBlock(strings.Join(code, "\n"), lang))
			continue
		}
		if m := headingRe.FindStringSubmatch(e); m != nil {
			if c := parseInline(strings.TrimSpace(m[2])); len(c) > 0 {
				out = append(out, Node{Type: "heading", Attrs: map[string]any{"level": len(m[1])}, Content: c})
			}
			i++
			continue
		}
		if ruleRe.MatchString(e) {
			out = append(out, Node{Type: "rule"})
			i++
			continue
		}
		if quoteRe.MatchString(e) {
			var inner []string
			for ; i < len(lines); i++ {
				m := quoteRe.FindStringSubmatch(expandTabs(lines[i]))
				if m == nil {
					break
				}
				inner = append(inner, m[1])
			}
			if q := quote(inner); q != nil {
				out = append(out, *q)
			}
			continue
		}
		if isItem(e) {
			var lists []Node
			lists, i = parseList(lines, i)
			out = append(out, lists...)
			continue
		}
		para := []string{lines[i]}
		i++
		if !strings.HasPrefix(strings.TrimSpace(e), "|") {
			for ; i < len(lines); i++ {
				n := expandTabs(lines[i])
				if isBlank(n) || startsBlock(n) {
					break
				}
				para = append(para, lines[i])
			}
		}
		if p := paragraph(para); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

func codeBlock(text, lang string) Node {
	n := Node{Type: "codeBlock"}
	if lang != "" {
		n.Attrs = map[string]any{"language": lang}
	}
	if text != "" {
		n.Content = []Node{{Type: "text", Text: text}}
	}
	return n
}

func quote(lines []string) *Node {
	var paras []Node
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			if p := paragraph(cur); p != nil {
				paras = append(paras, *p)
			}
			cur = nil
		}
	}
	for _, l := range lines {
		if isBlank(l) {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	if len(paras) == 0 {
		return nil
	}
	return &Node{Type: "blockquote", Content: paras}
}

func paragraph(lines []string) *Node {
	c := inlineLines(lines)
	if len(c) == 0 {
		return nil
	}
	return &Node{Type: "paragraph", Content: c}
}

// inlineLines joins lines with a space, or with a newline where the line ends
// in a hard break, and parses the result as inline content.
func inlineLines(lines []string) []Node {
	var sb strings.Builder
	for k, l := range lines {
		t := strings.TrimSpace(l)
		hard := false
		if k < len(lines)-1 {
			hard = strings.HasSuffix(l, "  ")
			if !hard {
				bs := len(t) - len(strings.TrimRight(t, `\`))
				if bs%2 == 1 {
					t = t[:len(t)-1]
					hard = true
				}
			}
		}
		sb.WriteString(t)
		switch {
		case k == len(lines)-1:
		case hard:
			sb.WriteByte('\n')
		default:
			sb.WriteByte(' ')
		}
	}
	return parseInline(sb.String())
}

type bItem struct {
	lines []string
	lists []*bList
}

type bList struct {
	ordered bool
	start   int
	items   []*bItem
}

type listLevel struct {
	indent int
	list   *bList
	parent *bItem
}

func newList(marker string) *bList {
	l := &bList{start: 1}
	if marker[0] >= '0' && marker[0] <= '9' {
		l.ordered = true
		if n, err := strconv.Atoi(marker[:len(marker)-1]); err == nil {
			l.start = n
		}
	}
	return l
}

func parseList(lines []string, i int) ([]Node, int) {
	var tops []*bList
	var stack []listLevel
	var last *bItem
	for i < len(lines) {
		e := expandTabs(lines[i])
		if isBlank(e) {
			j := i + 1
			for j < len(lines) && isBlank(lines[j]) {
				j++
			}
			if j < len(lines) && isItem(expandTabs(lines[j])) {
				i = j
				continue
			}
			break
		}
		if isItem(e) {
			m := itemRe.FindStringSubmatch(e)
			indent, marker := len(m[1]), m[2]
			ordered := marker[0] >= '0' && marker[0] <= '9'
			if len(stack) == 0 {
				l := newList(marker)
				tops = append(tops, l)
				stack = append(stack, listLevel{indent: indent, list: l})
			} else {
				var popped *listLevel
				for len(stack) > 1 && stack[len(stack)-1].indent > indent {
					p := stack[len(stack)-1]
					popped = &p
					stack = stack[:len(stack)-1]
				}
				top := stack[len(stack)-1]
				switch {
				case indent >= top.indent+2 && popped != nil:
					stack = append(stack, *popped)
				case indent >= top.indent+2 && len(stack) < maxListDepth:
					parent := top.list.items[len(top.list.items)-1]
					l := newList(marker)
					parent.lists = append(parent.lists, l)
					stack = append(stack, listLevel{indent: indent, list: l, parent: parent})
				}
				top = stack[len(stack)-1]
				if top.list.ordered != ordered {
					l := newList(marker)
					if top.parent == nil {
						tops = append(tops, l)
					} else {
						top.parent.lists = append(top.parent.lists, l)
					}
					stack[len(stack)-1].list = l
				}
			}
			item := &bItem{lines: []string{m[3]}}
			l := stack[len(stack)-1].list
			l.items = append(l.items, item)
			last = item
			i++
			continue
		}
		if startsBlock(e) {
			break
		}
		last.lines = append(last.lines, lines[i])
		i++
	}
	out := make([]Node, 0, len(tops))
	for _, l := range tops {
		out = append(out, l.node())
	}
	return out, i
}

func (l *bList) node() Node {
	n := Node{Type: "bulletList"}
	if l.ordered {
		n.Type = "orderedList"
		if l.start != 1 {
			n.Attrs = map[string]any{"order": l.start}
		}
	}
	for _, it := range l.items {
		li := Node{Type: "listItem"}
		if p := paragraph(it.lines); p != nil {
			li.Content = append(li.Content, *p)
		}
		for _, sub := range it.lists {
			li.Content = append(li.Content, sub.node())
		}
		if len(li.Content) > 0 {
			n.Content = append(n.Content, li)
		}
	}
	return n
}

func isEscapable(r rune) bool {
	return strings.ContainsRune("\\`*_{}[]()#+-.!><~", r)
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func isLinkable(href string) bool {
	l := strings.ToLower(href)
	for _, p := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(l, p) && len(l) > len(p) {
			return true
		}
	}
	return false
}

var markOrder = []string{"strong", "em", "code", "link"}

func markRank(t string) int {
	for i, m := range markOrder {
		if m == t {
			return i
		}
	}
	return len(markOrder)
}

func hasMark(marks []Mark, t string) bool {
	for _, m := range marks {
		if m.Type == t {
			return true
		}
	}
	return false
}

func withMark(marks []Mark, add Mark) []Mark {
	if hasMark(marks, add.Type) {
		return marks
	}
	out := make([]Mark, 0, len(marks)+1)
	placed := false
	for _, m := range marks {
		if !placed && markRank(add.Type) < markRank(m.Type) {
			out = append(out, add)
			placed = true
		}
		out = append(out, m)
	}
	if !placed {
		out = append(out, add)
	}
	return out
}

func marksEqual(a, b []Mark) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Attrs["href"] != b[i].Attrs["href"] {
			return false
		}
	}
	return true
}

func appendNode(out []Node, n Node) []Node {
	if k := len(out) - 1; k >= 0 && n.Type == "text" && out[k].Type == "text" && marksEqual(out[k].Marks, n.Marks) {
		out[k].Text += n.Text
		return out
	}
	return append(out, n)
}

func textNode(s string, marks []Mark) Node {
	n := Node{Type: "text", Text: s}
	if len(marks) > 0 {
		n.Marks = append([]Mark(nil), marks...)
	}
	return n
}

func parseInline(s string) []Node { return inlineRunes([]rune(s), nil) }

type scanner struct {
	rs    []rune
	steps int
	limit int
}

func (s *scanner) spent() bool {
	s.steps++
	return s.steps > s.limit
}

func runLen(rs []rune, i int) int {
	n := 0
	for i+n < len(rs) && rs[i+n] == rs[i] {
		n++
	}
	return n
}

// codeSpanEnd returns the index just past the code span opened by the
// backtick run of length m at i, or -1.
func (s *scanner) codeSpanEnd(i, m int) int {
	for k := i + m; k < len(s.rs); {
		if s.spent() {
			return -1
		}
		if s.rs[k] != '`' {
			k++
			continue
		}
		r := runLen(s.rs, k)
		if r == m {
			return k + r
		}
		k += r
	}
	return -1
}

func (s *scanner) canOpen(i, m int) bool {
	next := i + m
	if next >= len(s.rs) || unicode.IsSpace(s.rs[next]) {
		return false
	}
	return s.rs[i] != '_' || i == 0 || !isWordRune(s.rs[i-1])
}

func (s *scanner) canClose(i, m int) bool {
	if i == 0 || unicode.IsSpace(s.rs[i-1]) {
		return false
	}
	after := i + m
	return s.rs[i] != '_' || after >= len(s.rs) || !isWordRune(s.rs[after])
}

// bracketClose returns the index of the ] matching the [ at i, or -1.
func (s *scanner) bracketClose(i int) int {
	depth := 0
	for j := i; j < len(s.rs); {
		if s.spent() {
			return -1
		}
		switch c := s.rs[j]; {
		case c == '\\' && j+1 < len(s.rs) && isEscapable(s.rs[j+1]):
			j += 2
		case c == '`':
			m := runLen(s.rs, j)
			if e := s.codeSpanEnd(j, m); e >= 0 {
				j = e
			} else {
				j += m
			}
		case c == '[':
			depth++
			j++
		case c == ']':
			depth--
			if depth == 0 {
				return j
			}
			j++
		default:
			j++
		}
	}
	return -1
}

// linkDest reads a link destination whose first character is at j and
// returns it with the index past the closing parenthesis.
func linkDest(rs []rune, j int) (string, int, bool) {
	var sb []rune
	depth := 0
	for j < len(rs) {
		c := rs[j]
		switch {
		case c == '\\' && j+1 < len(rs) && isEscapable(rs[j+1]):
			sb = append(sb, rs[j+1])
			j += 2
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return string(sb), j + 1, true
			}
			depth--
		case unicode.IsSpace(c) || unicode.IsControl(c):
			return "", 0, false
		}
		sb = append(sb, c)
		j++
	}
	return "", 0, false
}

type inlineTok struct {
	nodes    []Node
	delim    rune
	count    int
	orig     int
	canOpen  bool
	canClose bool
	added    []Mark
}

func inlineRunes(rs []rune, marks []Mark) []Node {
	s := &scanner{rs: rs, limit: 64*len(rs) + 10000}
	var toks []inlineTok
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			toks = append(toks, inlineTok{nodes: []Node{textNode(string(buf), marks)}})
			buf = buf[:0]
		}
	}
	emit := func(nodes ...Node) {
		flush()
		toks = append(toks, inlineTok{nodes: nodes})
	}
	for i := 0; i < len(rs); {
		c := rs[i]
		switch c {
		case '\n':
			emit(Node{Type: "hardBreak"})
			i++
			continue
		case '\\':
			if i+1 < len(rs) && isEscapable(rs[i+1]) {
				buf = append(buf, rs[i+1])
				i += 2
			} else {
				buf = append(buf, c)
				i++
			}
			continue
		case '`':
			m := runLen(rs, i)
			if e := s.codeSpanEnd(i, m); e >= 0 {
				emit(codeSpan(string(rs[i+m:e-m]), marks))
				i = e
				continue
			}
			buf = append(buf, rs[i:i+m]...)
			i += m
			continue
		case '<':
			if j := indexRune(rs, i+1, '>'); j >= 0 {
				url := string(rs[i+1 : j])
				if isLinkable(url) && !strings.ContainsAny(url, " <\n\t") {
					emit(textNode(url, linkMarks(marks, url)))
					i = j + 1
					continue
				}
			}
		case '[':
			if end, nodes := s.link(i, marks); end > 0 {
				emit(nodes...)
				i = end
				continue
			}
		case '*', '_':
			m := runLen(rs, i)
			flush()
			toks = append(toks, inlineTok{delim: c, count: m, orig: m, canOpen: s.canOpen(i, m), canClose: s.canClose(i, m)})
			i += m
			continue
		}
		buf = append(buf, c)
		i++
	}
	flush()
	processEmphasis(toks)
	var out []Node
	for _, t := range toks {
		if t.delim != 0 {
			if t.count > 0 {
				out = appendNode(out, textNode(strings.Repeat(string(t.delim), t.count), withAll(marks, t.added)))
			}
			continue
		}
		for _, n := range t.nodes {
			if n.Type == "text" && !hasMark(n.Marks, "code") {
				for _, m := range t.added {
					n.Marks = withMark(n.Marks, m)
				}
			}
			out = appendNode(out, n)
		}
	}
	return out
}

func withAll(marks, extra []Mark) []Mark {
	for _, m := range extra {
		marks = withMark(marks, m)
	}
	return marks
}

// processEmphasis pairs delimiter runs the way CommonMark does, including
// the rule of three, and records the marks each pair adds to the tokens
// between its two runs.
func processEmphasis(toks []inlineTok) {
	bottom := map[[3]int]int{}
	for ci := range toks {
		c := &toks[ci]
		for c.delim != 0 && c.canClose && c.count > 0 {
			key := [3]int{int(c.delim), c.orig % 3, 0}
			if c.canOpen {
				key[2] = 1
			}
			floor := bottom[key]
			found := -1
			for oi := ci - 1; oi >= floor; oi-- {
				o := &toks[oi]
				if o.delim != c.delim || !o.canOpen || o.count == 0 {
					continue
				}
				if (o.canClose || c.canOpen) && (o.orig+c.orig)%3 == 0 && (o.orig%3 != 0 || c.orig%3 != 0) {
					continue
				}
				found = oi
				break
			}
			if found < 0 {
				bottom[key] = max(ci-1, 0)
				if !c.canOpen {
					c.canClose = false
				}
				break
			}
			o := &toks[found]
			use := 1
			mark := Mark{Type: "em"}
			if o.count >= 2 && c.count >= 2 {
				use = 2
				mark = Mark{Type: "strong"}
			}
			for k := found + 1; k < ci; k++ {
				toks[k].added = withMark(toks[k].added, mark)
				if toks[k].delim != 0 {
					toks[k].canOpen, toks[k].canClose = false, false
				}
			}
			o.count -= use
			c.count -= use
		}
	}
}

func indexRune(rs []rune, from int, r rune) int {
	for j := from; j < len(rs); j++ {
		if rs[j] == r {
			return j
		}
	}
	return -1
}

func linkMarks(marks []Mark, href string) []Mark {
	return withMark(marks, Mark{Type: "link", Attrs: map[string]any{"href": href}})
}

func codeSpan(content string, outer []Mark) Node {
	content = strings.ReplaceAll(content, "\n", " ")
	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.TrimSpace(content) != "" {
		content = content[1 : len(content)-1]
	}
	marks := []Mark{{Type: "code"}}
	for _, m := range outer {
		if m.Type == "link" {
			marks = append(marks, m)
		}
	}
	return Node{Type: "text", Text: content, Marks: marks}
}

// link parses an inline link starting at the [ at i. It returns the index
// past the link and its nodes, or 0 when there is no link there.
func (s *scanner) link(i int, marks []Mark) (int, []Node) {
	cl := s.bracketClose(i)
	if cl < 0 || cl+1 >= len(s.rs) || s.rs[cl+1] != '(' {
		return 0, nil
	}
	href, end, ok := linkDest(s.rs, cl+2)
	if !ok {
		return 0, nil
	}
	inner := marks
	if isLinkable(href) && !hasMark(marks, "link") {
		inner = linkMarks(marks, href)
	}
	nodes := inlineRunes(s.rs[i+1:cl], inner)
	if len(nodes) == 0 {
		return 0, nil
	}
	return end, nodes
}
