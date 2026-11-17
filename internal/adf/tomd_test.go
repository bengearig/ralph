package adf

import (
	"os"
	"strings"
	"testing"
)

func mustDoc(t *testing.T, blocks string) Doc {
	t.Helper()
	d, err := ParseJSON([]byte(`{"version":1,"type":"doc","content":` + blocks + `}`))
	if err != nil {
		t.Fatalf("bad test document: %v", err)
	}
	return d
}

func TestToMarkdownEachNode(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"paragraph", content(para(tx("a"))), "a"},
		{"two paragraphs", content(para(tx("a")), para(tx("b"))), "a\n\nb"},
		{"heading", content(heading(3, tx("T"))), "### T"},
		{"heading level clamped", `[{"type":"heading","attrs":{"level":9},"content":[` + tx("T") + `]}]`, "###### T"},
		{"heading without level", `[{"type":"heading","content":[` + tx("T") + `]}]`, "# T"},
		{"bullet list", content(ul(li(para(tx("a"))), li(para(tx("b"))))), "- a\n- b"},
		{"ordered list", content(node("orderedList", li(para(tx("a"))), li(para(tx("b"))))), "1. a\n2. b"},
		{"ordered list from order", `[{"type":"orderedList","attrs":{"order":4},"content":[` + li(para(tx("a"))) + `,` + li(para(tx("b"))) + `]}]`, "4. a\n5. b"},
		{"code block", content(code("go", "x := 1")), "```go\nx := 1\n```"},
		{"empty code block", content(code("", "")), "```\n```"},
		{"block quote", content(node("blockquote", para(tx("a")), para(tx("b")))), "> a\n>\n> b"},
		{"rule", content(para(tx("a")), mk("rule"), para(tx("b"))), "a\n\n---\n\nb"},
		{"text in list item without paragraph", content(ul(li(tx("a")))), "- a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToMarkdown(mustDoc(t, c.doc)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestToMarkdownNestedLists(t *testing.T) {
	d := mustDoc(t, content(ul(
		li(para(tx("a")), ul(li(para(tx("b")), node("orderedList", li(para(tx("c"))), li(para(tx("d"))))))),
		li(para(tx("e"))),
	)))
	want := "- a\n  - b\n    1. c\n    2. d\n- e"
	if got := ToMarkdown(d); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToMarkdownCodeFenceLonger(t *testing.T) {
	cases := []struct{ text, want string }{
		{"a", "```\na\n```"},
		{"``", "```\n``\n```"},
		{"```", "````\n```\n````"},
		{"x\n```\ny\n````\nz", "`````\nx\n```\ny\n````\nz\n`````"},
	}
	for _, c := range cases {
		if got := ToMarkdown(mustDoc(t, content(code("", c.text)))); got != c.want {
			t.Errorf("code %q: got %q, want %q", c.text, got, c.want)
		}
		if got := FromMarkdown(ToMarkdown(mustDoc(t, content(code("", c.text))))); len(got.Content) != 1 || got.Content[0].Content[0].Text != c.text {
			t.Errorf("code %q does not round trip: %#v", c.text, got.Content)
		}
	}
}

func TestToMarkdownMarks(t *testing.T) {
	strong, em, cd, lk := mk("strong"), mk("em"), mk("code"), link("https://x.y/a")
	cases := []struct{ name, inline, want string }{
		{"strong", marked("a", strong), "**a**"},
		{"em", marked("a", em), "*a*"},
		{"code", marked("a", cd), "`a`"},
		{"link", marked("a", lk), "[a](https://x.y/a)"},
		{"strong and em", marked("a", em, strong), "***a***"},
		{"link outermost", marked("a", cd, em, strong, lk), "[***`a`***](https://x.y/a)"},
		{"code in link", marked("a", cd, lk), "[`a`](https://x.y/a)"},
		{"adjacent runs share a mark", marked("a ", em) + "," + marked("b", em, strong) + "," + marked(" c", em), "*a **b** c*"},
		{"whitespace moves outside", tx("x") + "," + marked(" a ", strong) + "," + tx("y"), "x **a** y"},
		{"code with backtick", marked("a`b", cd), "``a`b``"},
		{"code starting with backtick", marked("`a", cd), "`` `a ``"},
		{"unknown mark ignored", marked("a", mk("underline")), "a"},
		{"link without href", marked("a", mk("link")), "a"},
		{"href with parens and space", marked("a", link("https://x.y/a_(b) c")), `[a](https://x.y/a_\(b\)%20c)`},
		{"escapes", tx(`a*b_c [d] \ ` + "`e` <f>"), "a\\*b_c \\[d\\] \\\\ \\`e\\` \\<f>"},
		{"underscore at word edge", tx("_a_ b_c"), `\_a\_ b_c`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToMarkdown(mustDoc(t, content(para(splitNodes(c.inline)...)))); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// splitNodes splits a comma-joined list of text node literals.
func splitNodes(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func TestToMarkdownHardBreak(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"middle", content(para(tx("a"), mk("hardBreak"), tx("b"))), "a  \nb"},
		{"trailing", content(para(tx("a"), mk("hardBreak"))), "a"},
		{"leading", content(para(mk("hardBreak"), tx("a"))), "a"},
		{"doubled", content(para(tx("a"), mk("hardBreak"), mk("hardBreak"), tx("b"))), "a  \nb"},
		{"in list item", content(ul(li(para(tx("a"), mk("hardBreak"), tx("b"))))), "- a  \n  b"},
		{"newline in text", content(para(tx("a\nb"))), "a b"},
		{"in heading", content(heading(1, tx("a"), mk("hardBreak"), tx("b"))), "# a b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToMarkdown(mustDoc(t, c.doc)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestToMarkdownUnknownNodesNeverFail(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"inlineCard", content(para(`{"type":"inlineCard","attrs":{"url":"https://x.y/1"}}`)), "https://x.y/1"},
		{"blockCard", content(`{"type":"blockCard","attrs":{"url":"https://x.y/2"}}`), "https://x.y/2"},
		{"mention", content(para(`{"type":"mention","attrs":{"id":"1","text":"Ana"}}`)), "@Ana"},
		{"mention with at", content(para(`{"type":"mention","attrs":{"id":"1","text":"@Ana"}}`)), "@Ana"},
		{"emoji text", content(para(`{"type":"emoji","attrs":{"shortName":":x:","text":"😄"}}`)), "😄"},
		{"emoji short name", content(para(`{"type":"emoji","attrs":{"shortName":":smile:"}}`)), ":smile:"},
		{"status", content(para(`{"type":"status","attrs":{"text":"Done"}}`)), "Done"},
		{"date", content(para(`{"type":"date","attrs":{"timestamp":"1582152559000"}}`)), "1582152559000"},
		{"date number", content(para(`{"type":"date","attrs":{"timestamp":1582152559000}}`)), "1582152559000"},
		{"panel", content(node("panel", para(tx("p")))), "p"},
		{"expand", content(node("expand", para(tx("e")))), "e"},
		{"table", content(node("table", node("tableRow", node("tableHeader", para(tx("h"))), node("tableCell", para(tx("c")))))), "h\n\nc"},
		{"mediaSingle", content(node("mediaSingle", `{"type":"media","attrs":{"id":"x"}}`)), "[attachment]"},
		{"mediaGroup", content(node("mediaGroup", `{"type":"media","attrs":{"id":"x"}}`)), "[attachment]"},
		{"media", content(`{"type":"media","attrs":{"id":"x"}}`), "[attachment]"},
		{"invented container", content(node("inventedThing", para(tx("kept")))), "kept"},
		{"invented with text", content(`{"type":"inventedText","text":"words"}`), "words"},
		{"invented inline in paragraph", content(para(tx("a "), `{"type":"inventedInline","text":"b"}`)), "a b"},
		{"list item holding panel", content(ul(li(node("panel", para(tx("p")))))), "- p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToMarkdown(mustDoc(t, c.doc)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestToMarkdownEmptyNodes(t *testing.T) {
	cases := []string{
		content(),
		content(`{"type":"paragraph"}`),
		content(`{"type":"inventedThing"}`),
		content(`{"type":"inlineCard"}`),
		content(`{"type":"inlineCard","attrs":{}}`),
		content(`{"type":"heading","attrs":{"level":2}}`),
		content(`{"type":"bulletList"}`),
		content(ul(`{"type":"listItem"}`)),
		content(`{"type":"blockquote"}`),
		content(`{"type":"table","content":[{"type":"tableRow"}]}`),
		content(`{"type":"mediaSingle"}`),
	}
	for _, c := range cases {
		if got := ToMarkdown(mustDoc(t, c)); got != "" {
			t.Errorf("%s: got %q, want empty", c, got)
		}
	}
	if got := ToMarkdown(Doc{}); got != "" {
		t.Errorf("zero Doc: got %q", got)
	}
}

func TestParseJSONRealisticDocument(t *testing.T) {
	raw, err := os.ReadFile("testdata/realistic.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"## Overview",
		"Reported by @Alex Doe on 1582152559000. See https://example.atlassian.net/browse/PROJ-1 and [**the docs**](https://example.com/docs) 😄 In progress",
		"*Heads up:* this is a panel.",
		"- first\n  3. nested\n- second",
		"Name\n\nValue\n\nalpha\n\n1",
		"hidden text",
		"[attachment]",
		"````go\nfmt.Println(\"hi\")\n```\nx\n````",
		"> quoted",
		"---",
		"https://example.com/card",
		"from the future",
	}, "\n\n")
	// The strong+link mark pair renders with the link outermost.
	if got := ToMarkdown(d); got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
	if again := Normalize(ToMarkdown(d)); again != Normalize(again) {
		t.Errorf("rendered document is not stable under Normalize")
	}
}

func TestParseJSONRejectsBadInput(t *testing.T) {
	for _, in := range []string{
		``, `not json`, `{`, `[]`, `"doc"`, `null`,
		`{"version":1,"type":"paragraph","content":[]}`,
		`{"version":1,"content":[]}`,
		`{"version":1,"type":"doc","content":"x"}`,
		`{"version":1,"type":"doc","content":[{"type":5}]}`,
	} {
		if _, err := ParseJSON([]byte(in)); err == nil {
			t.Errorf("ParseJSON(%q) succeeded, want an error", in)
		}
	}
	d, err := ParseJSON([]byte(`{"version":1,"type":"doc"}`))
	if err != nil || d.Content == nil || len(d.Content) != 0 {
		t.Errorf("document without content: %#v, %v", d, err)
	}
}
