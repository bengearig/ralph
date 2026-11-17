package chunk

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func split(text string, target int) []Chunk {
	return Split(Input{Kind: "task", Title: "T", Text: text}, target)
}

func texts(cs []Chunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}

func TestSplitShortTextOneChunk(t *testing.T) {
	cs := split("hello world", 100)
	want := []Chunk{{Seq: 0, Embed: "T\n\nhello world", Text: "hello world", Excerpt: "hello world"}}
	if !reflect.DeepEqual(cs, want) {
		t.Fatalf("got %#v, want %#v", cs, want)
	}
}

func TestSplitEmptyTextTitleOnly(t *testing.T) {
	for _, in := range []string{"", "  \n\n \t\n"} {
		cs := split(in, 100)
		if len(cs) != 1 || cs[0].Text != "" || cs[0].Embed != "T" || cs[0].Seq != 0 || cs[0].Excerpt != "" {
			t.Fatalf("%q: got %#v", in, cs)
		}
	}
}

func TestSplitBlocksFillToTarget(t *testing.T) {
	cs := split("aaaa\n\nbbbb\n\ncccc\n\ndddd", 10)
	if got, want := texts(cs), []string{"aaaa\n\nbbbb", "cccc\n\ndddd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	cs = split("aaaa\n\nbbbb", 10)
	if len(cs) != 1 {
		t.Fatalf("exactly at target must fit: %q", texts(cs))
	}
	cs = split("aaaa\n\nbbbb", 9)
	if len(cs) != 2 {
		t.Fatalf("over target must split: %q", texts(cs))
	}
	cs = split("aaaaaaaaaaaa\n\nb", 7)
	if len(cs) != 2 || cs[0].Text != "aaaaaaaaaaaa" {
		t.Fatalf("a block over the target but under twice it stays whole: %q", texts(cs))
	}
}

func TestSplitHeadingPathInEmbedText(t *testing.T) {
	text := "# Top\nintro\n\n## Sub\nsub text\n\n### Deep\ndeep text\n\n## Other\nother text\n\n# Next\nnext text"
	cs := split(text, 20)
	var embeds []string
	for _, c := range cs {
		embeds = append(embeds, strings.SplitN(c.Embed, "\n\n", 2)[0])
	}
	want := []string{"T\nTop", "T\nTop > Sub", "T\nTop > Sub > Deep", "T\nTop > Other", "T\nNext"}
	if !reflect.DeepEqual(embeds, want) {
		t.Fatalf("got %q, want %q", embeds, want)
	}
	if cs[0].Embed != "T\nTop\n\n# Top\nintro" {
		t.Fatalf("embed = %q", cs[0].Embed)
	}
	if got := split("plain", 10)[0].Embed; got != "T\n\nplain" {
		t.Fatalf("no heading: %q", got)
	}
	if h, _ := heading("#nospace"); h != 0 {
		t.Fatal("heading needs a space")
	}
	if h, _ := heading("####### seven"); h != 0 {
		t.Fatal("seven hashes are not a heading")
	}
}

func TestSplitFencedBlockStaysWhole(t *testing.T) {
	code := "```go\nfunc a() {}\n\n# not a heading\n\nfunc b() {}\n```"
	cs := split("before\n\n"+code+"\nafter fence\n\nend", 1000)
	if len(cs) != 1 {
		t.Fatalf("got %q", texts(cs))
	}
	cs = split("x\n\n"+code+"\n\ny", 30)
	found := false
	for _, c := range cs {
		if strings.Contains(c.Text, code) {
			found = true
		}
		if strings.Contains(c.Embed, " > ") {
			t.Errorf("heading inside fence changed the path: %q", c.Embed)
		}
	}
	if !found {
		t.Fatalf("fenced block was cut: %q", texts(cs))
	}
	open := split("a\n\n~~~\nnever closed\n\nstill code", 1000)
	if len(blocksOf("a\n\n~~~\nnever closed\n\nstill code")) != 2 || len(open) != 1 {
		t.Fatal("an unclosed fence runs to the end")
	}
}

func TestSplitLongBlockAtSentences(t *testing.T) {
	s := strings.Repeat("Alpha beta gamma. ", 3) + "Why? " + "Stop! " + "Done.\nNext line"
	cs := split(s, 20)
	for _, c := range cs {
		if utf8.RuneCountInString(c.Text) > 20 {
			t.Errorf("chunk over target: %q", c.Text)
		}
	}
	for _, c := range cs[:len(cs)-1] {
		if !strings.HasSuffix(c.Text, ".") && !strings.HasSuffix(c.Text, "?") && !strings.HasSuffix(c.Text, "!") {
			t.Errorf("not cut at a sentence end: %q", c.Text)
		}
	}
	if len(cs) < 3 {
		t.Fatalf("got %q", texts(cs))
	}
}

func TestSplitLongBlockAtSpaces(t *testing.T) {
	s := strings.TrimSpace(strings.Repeat("word ", 40))
	cs := split(s, 20)
	for _, c := range cs {
		if utf8.RuneCountInString(c.Text) > 20 || !strings.HasPrefix(c.Text, "word") || !strings.HasSuffix(c.Text, "word") {
			t.Errorf("bad chunk %q", c.Text)
		}
	}
	if len(cs) < 10 {
		t.Fatalf("got %d chunks", len(cs))
	}
}

func TestSplitLongWordAtRuneBoundary(t *testing.T) {
	long := strings.Repeat("héé世界🙂", 30)
	cs := split("start "+long+" end", 7)
	var joined strings.Builder
	for _, c := range cs {
		if !utf8.ValidString(c.Text) || !utf8.ValidString(c.Excerpt) || !utf8.ValidString(c.Embed) {
			t.Fatalf("invalid utf-8 in %#v", c)
		}
		if utf8.RuneCountInString(c.Text) > 7 {
			t.Errorf("over target: %q", c.Text)
		}
		joined.WriteString(c.Text)
	}
	if got := strings.Join(strings.Fields(joined.String()), ""); got != "start"+long+"end" {
		t.Fatalf("text lost or reordered: %q", got)
	}
}

func TestSplitDeterministic(t *testing.T) {
	text := randomMarkdown(rand.New(rand.NewSource(7)))
	a, b := split(text, 80), split(text, 80)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs differ")
	}
}

func nonBlank(s string) []string {
	var out []string
	for _, r := range s {
		if !unicode.IsSpace(r) {
			out = append(out, string(r))
		}
	}
	sort.Strings(out)
	return out
}

func randomMarkdown(r *rand.Rand) string {
	words := []string{"alpha", "beta", "gamma.", "delta?", "épsilon", "世界", "🙂", "x", "supercalifragilisticexpialidocious", "ß!"}
	var b strings.Builder
	for i, n := 0, 1+r.Intn(30); i < n; i++ {
		switch r.Intn(6) {
		case 0:
			fmt.Fprintf(&b, "%s %s\n", strings.Repeat("#", 1+r.Intn(6)), words[r.Intn(len(words))])
		case 1:
			b.WriteString("```\n")
			for j := 0; j < r.Intn(8); j++ {
				b.WriteString(words[r.Intn(len(words))] + "\n")
				if r.Intn(3) == 0 {
					b.WriteString("\n")
				}
			}
			if r.Intn(4) != 0 {
				b.WriteString("```\n")
			}
		default:
			for j, m := 0, 1+r.Intn(60); j < m; j++ {
				b.WriteString(words[r.Intn(len(words))])
				switch r.Intn(8) {
				case 0:
					b.WriteString("\n")
				case 1:
					b.WriteString("\r\n")
				default:
					b.WriteString(" ")
				}
			}
		}
		b.WriteString(strings.Repeat("\n", r.Intn(3)))
	}
	return b.String()
}

func TestSplitCoversAllText(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		text := randomMarkdown(r)
		target := 5 + r.Intn(300)
		cs := split(text, target)
		var all strings.Builder
		for _, c := range cs {
			all.WriteString(c.Text)
		}
		if len(cs) < MaxChunks && !reflect.DeepEqual(nonBlank(all.String()), nonBlank(text)) {
			t.Fatalf("case %d (target %d): characters differ\ninput: %q\nchunks: %q", i, target, text, texts(cs))
		}
	}
}

func TestSplitCapsAt200Chunks(t *testing.T) {
	text := strings.Repeat("paragraph\n\n", 500)
	cs := split(text, 10)
	if len(cs) != MaxChunks || cs[MaxChunks-1].Seq != MaxChunks-1 {
		t.Fatalf("got %d chunks", len(cs))
	}
	if len(split(strings.Repeat("p\n\n", 200), 1)) != MaxChunks {
		t.Fatal("exactly the cap must be kept")
	}
}

func TestSplitExcerptFirst240CutAtWord(t *testing.T) {
	text := strings.Repeat("word  \n ", 100)
	c := split(text, 5000)[0]
	if !strings.HasSuffix(c.Excerpt, "word…") || utf8.RuneCountInString(c.Excerpt) > 241 || strings.Contains(c.Excerpt, "  ") || strings.Contains(c.Excerpt, "\n") {
		t.Fatalf("excerpt = %q", c.Excerpt)
	}
	if want := strings.TrimSuffix(c.Excerpt, "…"); !strings.HasPrefix(strings.Join(strings.Fields(text), " "), want) {
		t.Fatalf("excerpt is not a prefix: %q", c.Excerpt)
	}
	mid := strings.Repeat("a", 235) + " bbbbbbbbbb"
	if got := split(mid, 5000)[0].Excerpt; got != strings.Repeat("a", 235)+"…" {
		t.Fatalf("mid-word cut = %q", got)
	}
	solid := strings.Repeat("é", 300)
	if got := split(solid, 5000)[0].Excerpt; got != strings.Repeat("é", 240)+"…" {
		t.Fatalf("one long word = %q", got)
	}
	exact := strings.Repeat("a", 240)
	if got := split(exact, 5000)[0].Excerpt; got != exact {
		t.Fatalf("exact length should not be cut: %q", got)
	}
}

func TestSplitCRLFNormalized(t *testing.T) {
	a := split("# H\r\none\r\ntwo\r\n\r\nthree\rfour", 1000)
	b := split("# H\none\ntwo\n\nthree\nfour", 1000)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%#v != %#v", a, b)
	}
	if strings.Contains(a[0].Text, "\r") {
		t.Fatal("carriage return kept")
	}
}

func TestSplitSeqIncreasing(t *testing.T) {
	cs := split(strings.Repeat("some words here\n\n", 40), 30)
	if len(cs) < 5 {
		t.Fatalf("got %d chunks", len(cs))
	}
	for i, c := range cs {
		if c.Seq != i {
			t.Fatalf("chunk %d has seq %d", i, c.Seq)
		}
	}
}
