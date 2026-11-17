package adf

import (
	"encoding/json"
	"testing"
)

func TestFromMarkdownParagraphs(t *testing.T) {
	runContentCases(t, []mdCase{
		{"single", "hello", content(para(tx("hello")))},
		{"joined lines", "a\n  b \nc", content(para(tx("a b c")))},
		{"blank line between", "a\n\nb", content(para(tx("a")), para(tx("b")))},
		{"many blank lines", "a\n\n\n\nb", content(para(tx("a")), para(tx("b")))},
		{"hard break two spaces", "a  \nb", content(para(tx("a"), mk("hardBreak"), tx("b")))},
		{"hard break backslash", "a\\\nb", content(para(tx("a"), mk("hardBreak"), tx("b")))},
		{"escaped backslash is no break", "a\\\\\nb", content(para(tx(`a\ b`)))},
		{"one trailing space is no break", "a \nb", content(para(tx("a b")))},
		{"trailing break on last line", "a  ", content(para(tx("a")))},
		{"trailing backslash on last line", "a\\", content(para(tx(`a\`)))},
	})
}

func TestFromMarkdownHeadings(t *testing.T) {
	var cases []mdCase
	for i := 1; i <= 6; i++ {
		hashes := ""
		for j := 0; j < i; j++ {
			hashes += "#"
		}
		cases = append(cases, mdCase{"level " + hashes, hashes + " Title", content(heading(i, tx("Title")))})
	}
	cases = append(cases,
		mdCase{"seven hashes", "####### Title", content(para(tx("####### Title")))},
		mdCase{"no space", "#Title", content(para(tx("#Title")))},
		mdCase{"only hash", "#", content(para(tx("#")))},
		mdCase{"inline marks", "## a **b**", content(heading(2, tx("a "), marked("b", mk("strong"))))},
		mdCase{"interrupts paragraph", "a\n# b", content(para(tx("a")), heading(1, tx("b")))},
	)
	runContentCases(t, cases)
}

func TestFromMarkdownBulletAndOrderedLists(t *testing.T) {
	runContentCases(t, []mdCase{
		{"bullets", "- a\n- b", content(node("bulletList", li(para(tx("a"))), li(para(tx("b")))))},
		{"star and plus", "* a\n+ b", content(node("bulletList", li(para(tx("a"))), li(para(tx("b")))))},
		{"ordered dot", "1. a\n2. b", content(node("orderedList", li(para(tx("a"))), li(para(tx("b")))))},
		{"ordered paren", "1) a\n2) b", content(node("orderedList", li(para(tx("a"))), li(para(tx("b")))))},
		{"ordered start", "3. a\n4. b", `[{"type":"orderedList","attrs":{"order":3},"content":[` + li(para(tx("a"))) + `,` + li(para(tx("b"))) + `]}]`},
		{"continuation line", "- a\nb", content(node("bulletList", li(para(tx("a b")))))},
		{"blank between items", "- a\n\n- b", content(node("bulletList", li(para(tx("a"))), li(para(tx("b")))))},
		{"paragraph after blank ends list", "- a\n\nb", content(node("bulletList", li(para(tx("a")))), para(tx("b")))},
		{"bullet then ordered", "- a\n1. b", content(node("bulletList", li(para(tx("a")))), node("orderedList", li(para(tx("b")))))},
		{"empty marker is text", "-", content(para(tx("-")))},
		{"item marks", "- **a** b", content(node("bulletList", li(para(marked("a", mk("strong")), tx(" b")))))},
	})
}

func ul(items ...string) string { return node("bulletList", items...) }

func TestFromMarkdownNestedLists(t *testing.T) {
	runContentCases(t, []mdCase{
		{"two spaces", "- a\n  - b", content(ul(li(para(tx("a")), ul(li(para(tx("b")))))))},
		{"four spaces", "- a\n    - b", content(ul(li(para(tx("a")), ul(li(para(tx("b")))))))},
		{"one space is a sibling", "- a\n - b", content(ul(li(para(tx("a"))), li(para(tx("b")))))},
		{"dedent", "- a\n  - b\n- c", content(ul(li(para(tx("a")), ul(li(para(tx("b"))))), li(para(tx("c")))))},
		{"mixed ordered inside bullet", "- a\n  1. b", content(ul(li(para(tx("a")), node("orderedList", li(para(tx("b")))))))},
		{"four levels", "- a\n  - b\n    - c\n      - d", content(
			ul(li(para(tx("a")), ul(li(para(tx("b")), ul(li(para(tx("c")), ul(li(para(tx("d")))))))))))},
		{"deeper than four is level four", "- a\n  - b\n    - c\n      - d\n        - e\n          - f", content(
			ul(li(para(tx("a")), ul(li(para(tx("b")), ul(li(para(tx("c")), ul(
				li(para(tx("d"))), li(para(tx("e"))), li(para(tx("f")))))))))))},
		{"dedent between levels", "- a\n    - b\n  - c", content(
			ul(li(para(tx("a")), ul(li(para(tx("b"))), li(para(tx("c")))))))},
	})
}

func TestFromMarkdownMixedListMarkers(t *testing.T) {
	runContentCases(t, []mdCase{
		{"all bullet markers", "- a\n* b\n+ c", content(node("bulletList", li(para(tx("a"))), li(para(tx("b"))), li(para(tx("c")))))},
		{"mixed number styles", "1. a\n2) b", content(node("orderedList", li(para(tx("a"))), li(para(tx("b")))))},
		{"nested mixed markers", "- a\n  * b\n  + c", content(node("bulletList", li(para(tx("a")),
			node("bulletList", li(para(tx("b"))), li(para(tx("c")))))))},
	})
}

func TestFromMarkdownFencedCode(t *testing.T) {
	runContentCases(t, []mdCase{
		{"backticks", "```\nx\ny\n```", content(code("", "x\ny"))},
		{"tildes", "~~~\nx\n~~~", content(code("", "x"))},
		{"language", "```go\nx := 1\n```", content(code("go", "x := 1"))},
		{"language with extra words", "```go title=x\nx\n```", content(code("go", "x"))},
		{"unclosed", "```\nx\ny", content(code("", "x\ny"))},
		{"empty", "```\n```", content(code("", ""))},
		{"markup left alone", "```\n# h **b** - l `c` [a](http://x.y)\n```", content(code("", "# h **b** - l `c` [a](http://x.y)"))},
		{"longer fence holds shorter", "````\n```\nx\n```\n````", content(code("", "```\nx\n```"))},
		{"mismatched fence does not close", "```\nx\n~~~\ny\n```", content(code("", "x\n~~~\ny"))},
		{"tabs kept", "```\n\tx\n```", content(code("", "\tx"))},
		{"between paragraphs", "a\n```\nx\n```\nb", content(para(tx("a")), code("", "x"), para(tx("b")))},
		{"backtick info with backtick is text", "```a`b", content(para(tx("```a`b")))},
	})
}

func TestFromMarkdownBlockQuote(t *testing.T) {
	runContentCases(t, []mdCase{
		{"single", "> a", content(node("blockquote", para(tx("a"))))},
		{"joined lines", "> a\n> b", content(node("blockquote", para(tx("a b"))))},
		{"no space after marker", ">a", content(node("blockquote", para(tx("a"))))},
		{"two paragraphs", "> a\n>\n> b", content(node("blockquote", para(tx("a")), para(tx("b"))))},
		{"hard break", "> a  \n> b", content(node("blockquote", para(tx("a"), mk("hardBreak"), tx("b"))))},
		{"inline marks", "> **a**", content(node("blockquote", para(marked("a", mk("strong")))))},
		{"empty quote", ">", content()},
	})
}

func TestFromMarkdownRule(t *testing.T) {
	runContentCases(t, []mdCase{
		{"dashes", "---", content(mk("rule"))},
		{"stars", "***", content(mk("rule"))},
		{"underscores", "___", content(mk("rule"))},
		{"spaced", "- - -", content(mk("rule"))},
		{"long", "----------", content(mk("rule"))},
		{"two is text", "--", content(para(tx("--")))},
		{"between paragraphs", "a\n---\nb", content(para(tx("a")), mk("rule"), para(tx("b")))},
	})
}

func TestFromMarkdownTableBecomesParagraphs(t *testing.T) {
	runContentCases(t, []mdCase{
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", content(
			para(tx("| a | b |")), para(tx("|---|---|")), para(tx("| 1 | 2 |")))},
		{"table after paragraph", "x\n| a |", content(para(tx("x")), para(tx("| a |")))},
	})
}

func TestFromMarkdownInlineMarks(t *testing.T) {
	strong, em, cd := mk("strong"), mk("em"), mk("code")
	runContentCases(t, []mdCase{
		{"strong stars", "a **b** c", content(para(tx("a "), marked("b", strong), tx(" c")))},
		{"strong underscores", "__b__", content(para(marked("b", strong)))},
		{"em star", "*b*", content(para(marked("b", em)))},
		{"em underscore", "_b_", content(para(marked("b", em)))},
		{"code", "a `b` c", content(para(tx("a "), marked("b", cd), tx(" c")))},
		{"double backtick code", "`` a`b ``", content(para(marked("a`b", cd)))},
		{"nested strong in em", "*a **b** c*", content(para(marked("a ", em), marked("b", strong, em), marked(" c", em)))},
		{"nested em in strong", "**a *b***", content(para(marked("a ", strong), marked("b", strong, em)))},
		{"triple", "***a***", content(para(marked("a", strong, em)))},
		{"code with other markers inside", "`a **b** [c](http://x.y) _d_`", content(para(marked("a **b** [c](http://x.y) _d_", cd)))},
		{"code inside strong keeps no strong", "**`a`**", content(para(marked("a", cd)))},
		{"strong inside link", "[**a**](http://x.y)", content(para(marked("a", strong, link("http://x.y"))))},
		{"code inside link", "[`a`](http://x.y)", content(para(marked("a", cd, link("http://x.y"))))},
		{"marks across soft break", "**a\nb**", content(para(marked("a b", strong)))},
	})
}

func TestFromMarkdownSnakeCaseNotItalic(t *testing.T) {
	runContentCases(t, []mdCase{
		{"snake", "snake_case_name", content(para(tx("snake_case_name")))},
		{"leading underscore word", "_private_ and snake_case", content(para(marked("private", mk("em")), tx(" and snake_case")))},
		{"double", "a__b__c", content(para(tx("a__b__c")))},
	})
}

func TestFromMarkdownLinks(t *testing.T) {
	runContentCases(t, []mdCase{
		{"https", "[a](https://x.y/z)", content(para(marked("a", link("https://x.y/z"))))},
		{"http", "[a](http://x.y)", content(para(marked("a", link("http://x.y"))))},
		{"mailto", "[a](mailto:a@b.c)", content(para(marked("a", link("mailto:a@b.c"))))},
		{"uppercase scheme", "[a](HTTPS://X.Y)", content(para(marked("a", link("HTTPS://X.Y"))))},
		{"javascript dropped", "[a](javascript:alert(1))", content(para(tx("a")))},
		{"relative dropped", "[a](/rel/path)", content(para(tx("a")))},
		{"data dropped", "[a](data:text/html,x)", content(para(tx("a")))},
		{"balanced parens", "[a](https://x.y/a_(b))", content(para(marked("a", link("https://x.y/a_(b)"))))},
		{"escaped paren", `[a](https://x.y/a\)b)`, content(para(marked("a", link("https://x.y/a)b"))))},
		{"text around", "see [a](http://x.y) now", content(para(tx("see "), marked("a", link("http://x.y")), tx(" now")))},
		{"brackets without target stay text", "[a] and [b]", content(para(tx("[a] and [b]")))},
		{"space in target is text", "[a](http://x.y z)", content(para(tx("[a](http://x.y z)")))},
		{"empty text stays text", "[](http://x.y)", content(para(tx("[](http://x.y)")))},
	})
}

func TestFromMarkdownAutolink(t *testing.T) {
	runContentCases(t, []mdCase{
		{"https", "<https://x.y/z>", content(para(marked("https://x.y/z", link("https://x.y/z"))))},
		{"mailto", "<mailto:a@b.c>", content(para(marked("mailto:a@b.c", link("mailto:a@b.c"))))},
		{"inside text", "a <http://x.y> b", content(para(tx("a "), marked("http://x.y", link("http://x.y")), tx(" b")))},
		{"other scheme is text", "<ftp://x.y>", content(para(tx("<ftp://x.y>")))},
		{"not closed", "<https://x.y", content(para(tx("<https://x.y")))},
	})
}

func TestFromMarkdownEscapes(t *testing.T) {
	runContentCases(t, []mdCase{
		{"stars", `\*a\*`, content(para(tx("*a*")))},
		{"underscores", `\_a\_`, content(para(tx("_a_")))},
		{"backtick", "\\`a\\`", content(para(tx("`a`")))},
		{"brackets", `\[a\](http://x.y)`, content(para(tx("[a](http://x.y)")))},
		{"backslash", `a\\b`, content(para(tx(`a\b`)))},
		{"punctuation", `\{\}\(\)\#\+\-\.\!\>`, content(para(tx("{}()#+-.!>")))},
		{"hash at line start", `\# not a heading`, content(para(tx("# not a heading")))},
		{"dash at line start", `\- not a list`, content(para(tx("- not a list")))},
		{"number at line start", `1\. not a list`, content(para(tx("1. not a list")))},
		{"not escapable", `\a\b`, content(para(tx(`\a\b`)))},
		{"escaped angle", `\<https://x.y>`, content(para(tx("<https://x.y>")))},
	})
}

func TestFromMarkdownUnmatchedMarkers(t *testing.T) {
	runContentCases(t, []mdCase{
		{"lone star", "a * b", content(para(tx("a * b")))},
		{"unclosed strong", "**a", content(para(tx("**a")))},
		{"unclosed em", "*a", content(para(tx("*a")))},
		{"unclosed code", "`a", content(para(tx("`a")))},
		{"unclosed bracket", "[a", content(para(tx("[a")))},
		{"space inside markers", "** a **", content(para(tx("** a **")))},
		{"lone underscore", "a _ b", content(para(tx("a _ b")))},
		{"mismatched run", "**a*", content(para(tx("*"), marked("a", mk("em"))))},
	})
}

func TestFromMarkdownCRLF(t *testing.T) {
	want := content(para(tx("a"), mk("hardBreak"), tx("b")), heading(1, tx("h")), node("bulletList", li(para(tx("x")))))
	assertContent(t, FromMarkdown("a  \r\nb\r\n\r\n# h\r\n- x"), want)
	assertContent(t, FromMarkdown("a  \rb\r\r# h\r- x"), want)
}

func TestFromMarkdownEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n", " \t\n \r\n"} {
		d := FromMarkdown(in)
		if d.Content == nil || len(d.Content) != 0 {
			t.Errorf("FromMarkdown(%q).Content = %#v, want empty non-nil", in, d.Content)
		}
		raw, _ := json.Marshal(d)
		if string(raw) != `{"version":1,"type":"doc","content":[]}` {
			t.Errorf("FromMarkdown(%q) marshals to %s", in, raw)
		}
		if got := Normalize(in); got != "" {
			t.Errorf("Normalize(%q) = %q, want empty", in, got)
		}
	}
}

func TestFromMarkdownUnicode(t *testing.T) {
	runContentCases(t, []mdCase{
		{"multi-byte", "héllo wörld 日本語", content(para(tx("héllo wörld 日本語")))},
		{"emoji", "ship it 🚀 **now 🎉**", content(para(tx("ship it 🚀 "), marked("now 🎉", mk("strong"))))},
		{"emoji in code", "`🚀`", content(para(marked("🚀", mk("code"))))},
		{"emoji link text", "[🚀](https://x.y)", content(para(marked("🚀", link("https://x.y"))))},
		{"unicode snake", "größe_wert_x", content(para(tx("größe_wert_x")))},
		{"heading", "# Ünïcode ✓", content(heading(1, tx("Ünïcode ✓")))},
	})
}

func TestFromMarkdownMarshalsDocumentedShape(t *testing.T) {
	d := FromMarkdown("# T\n\ntext **b**")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != float64(1) || got["type"] != "doc" {
		t.Fatalf("root = %v", got)
	}
	if c, ok := got["content"].([]any); !ok || len(c) != 2 {
		t.Fatalf("content = %v", got["content"])
	}
	want := `{"version":1,"type":"doc","content":[{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"T"}]},` +
		`{"type":"paragraph","content":[{"type":"text","text":"text "},{"type":"text","text":"b","marks":[{"type":"strong"}]}]}]}`
	var exp any
	_ = json.Unmarshal([]byte(want), &exp)
	var act any
	_ = json.Unmarshal(raw, &act)
	if !equalJSON(exp, act) {
		t.Errorf("shape mismatch\n got: %s\nwant: %s", raw, want)
	}
	zero, _ := json.Marshal(Doc{})
	if string(zero) != `{"version":1,"type":"doc","content":[]}` {
		t.Errorf("zero Doc marshals to %s", zero)
	}
}
