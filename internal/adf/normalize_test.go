package adf

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

var vocabulary = []string{"alpha", "beta", "gamma", "delta", "omega", "kappa", "sigma", "zeta", "héllo", "日本語", "snake_case", "x1"}

var markup = []string{
	"*", "**", "***", "_", "__", "`", "``", "```", "~~~", "[", "]", "](http://x.y/z)", "(", ")", "<", ">", "<https://x.y/q>",
	"\\", "\\*", "#", "## ", "- ", "* ", "+ ", "1. ", "2) ", "> ", "---", "| ", "!", "  \n", "\\\n", "\n", "\n\n", "\n  ", "\n    ", " ", "  ", "\t",
}

func generate(r *rand.Rand, withWords bool) (string, []string) {
	var sb strings.Builder
	var words []string
	for i, n := 0, 1+r.Intn(24); i < n; i++ {
		if r.Intn(3) == 0 {
			w := vocabulary[r.Intn(len(vocabulary))]
			words = append(words, w)
			sb.WriteString(w)
		} else {
			sb.WriteString(markup[r.Intn(len(markup))])
		}
		if r.Intn(3) == 0 {
			sb.WriteByte(' ')
		}
	}
	return sb.String(), words
}

func TestNormalizeIsIdempotent(t *testing.T) {
	fixed := []string{
		"", "plain", "# T\n\ntext **b** _i_ `c`\n\n- a\n  - b\n\n> q\n\n```go\nx\n```\n\n---",
		"a  \nb\\\nc", "1. a\n2. b\n   - c", "* a\n+ b\n- c", "[a](http://x.y) <http://x.y>",
		"\\# x\n\n\\- y\n\n1\\. z", "- - -\n\n- \\- x", "*a **b** c*", "***a***", "a_b_c __d__ e",
		"| a | b |\n|---|---|", "~~~\n```\n~~~", "````\n```\n````", "> - x\n> # y", "- a\n\nb\n\n- c",
	}
	for _, in := range fixed {
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Errorf("not idempotent for %q\nonce:  %q\ntwice: %q", in, once, twice)
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		in, _ := generate(r, false)
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Fatalf("not idempotent for %q\nonce:  %q\ntwice: %q", in, once, twice)
		}
	}
}

func TestNormalizeKeepsContent(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 20000; i++ {
		in, words := generate(r, true)
		if strings.Contains(in, "```") || strings.Contains(in, "~~~") {
			continue
		}
		out := Normalize(in)
		for _, w := range words {
			if !strings.Contains(out, w) {
				t.Fatalf("word %q lost\ninput:  %q\noutput: %q", w, in, out)
			}
		}
	}
}

func TestRoundTripStable(t *testing.T) {
	for _, in := range []string{
		"plain text",
		"# Title\n\n## Sub",
		"a  \nb",
		"**bold** *italic* `code` [link](https://x.y/z) ***both***",
		"*a **b** c*",
		"- a\n- b",
		"1. a\n2. b",
		"- a\n  - b\n    1. c\n    2. d\n- e",
		"> quoted\n>\n> again",
		"```go\nx := 1\n```",
		"````\n```\nx\n```\n````",
		"---",
		"a\n\n---\n\nb",
		"\\*not italic\\*",
		"snake_case_name",
		"\\# text\n\n\\- text\n\n1\\. text",
		"| a | b |\n\n|---|---|",
		"[a](https://x.y/a_\\(b\\))",
		"3. a\n4. b",
	} {
		if got := ToMarkdown(FromMarkdown(in)); got != in {
			t.Errorf("round trip changed text\n in:  %q\nout: %q", in, got)
		}
	}
}

func TestLimitCutsAtLineBreak(t *testing.T) {
	line := strings.Repeat("a", 30)
	in := strings.Join([]string{line, line, line, line, line}, "\n")
	got := Limit(in, 132)
	want := strings.Join([]string{line, line, line}, "\n") + "\n\n" + truncationNote
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if n := utf8.RuneCountInString(got); n > 132 {
		t.Errorf("result has %d runes", n)
	}
}

func TestLimitAddsNote(t *testing.T) {
	got := Limit(strings.Repeat("word ", 100), 120)
	if !strings.HasSuffix(got, "\n\n[truncated: the full text is in ralph]") {
		t.Errorf("no truncation note: %q", got)
	}
	if utf8.RuneCountInString(got) > 120 {
		t.Errorf("too long: %d", utf8.RuneCountInString(got))
	}
	long := strings.Repeat("x", 500)
	if got := Limit(long, 150); utf8.RuneCountInString(got) > 150 || !strings.HasSuffix(got, truncationNote) || !strings.HasPrefix(got, "xxx") {
		t.Errorf("single long line: %q", got)
	}
}

func TestLimitShortInputUnchanged(t *testing.T) {
	for _, in := range []string{"", "short", strings.Repeat("a", 100), "a\nb\n"} {
		if got := Limit(in, 100); got != in {
			t.Errorf("Limit(%q) = %q", in, got)
		}
	}
	in := strings.Repeat("a", 500)
	if got := Limit(in, 500); got != in {
		t.Errorf("exactly at the limit changed")
	}
}

func TestLimitCountsRunes(t *testing.T) {
	in := strings.Repeat("日本語", 40)
	if got := Limit(in, 120); got != in {
		t.Errorf("120 runes within a 120 limit were cut")
	}
	got := Limit(in+"日", 120)
	if n := utf8.RuneCountInString(got); n > 120 {
		t.Errorf("result has %d runes", n)
	}
	if !strings.HasSuffix(got, truncationNote) || !strings.HasPrefix(got, "日本語") || !utf8.ValidString(got) {
		t.Errorf("bad result %q", got)
	}
	emoji := strings.Repeat("🚀 ", 200)
	if got := Limit(emoji, 100); !utf8.ValidString(got) || utf8.RuneCountInString(got) > 100 {
		t.Errorf("emoji cut badly: %q", got)
	}
}

func TestLimitMinimum(t *testing.T) {
	in := strings.Repeat("abcdefghi\n", 50)
	for _, max := range []int{-5, 0, 1, 99, 100} {
		got := Limit(in, max)
		if got != Limit(in, 100) {
			t.Errorf("max %d is not treated as 100", max)
		}
		if n := utf8.RuneCountInString(got); n > 100 || !strings.HasSuffix(got, truncationNote) {
			t.Errorf("max %d: %d runes %q", max, n, got)
		}
	}
	short := strings.Repeat("a", 100)
	if Limit(short, 5) != short {
		t.Errorf("100 characters must fit when max is raised to 100")
	}
}

func FuzzFromMarkdown(f *testing.F) {
	for _, s := range []string{
		"", "# t", "**a** _b_ `c`", "- a\n  - b", "```\nx", "[a](b)", "<https://x.y>", "> q", "a  \nb",
		"***", "[[[[", "`````", "\\", "1. a\n2) b", "| a |", "\r\n", "🚀 **x**",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		d := FromMarkdown(in)
		if d.Content == nil || d.Type != "doc" || d.Version != 1 {
			t.Fatalf("bad root for %q", in)
		}
		md := ToMarkdown(d)
		if again := Normalize(md); again != md {
			t.Fatalf("not idempotent for %q\nonce:  %q\ntwice: %q", in, md, again)
		}
	})
}
