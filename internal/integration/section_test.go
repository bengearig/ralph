package integration

import (
	"strings"
	"testing"
)

const block = BeginMarker + "\nbody\n" + EndMarker + "\n"

func TestFindSectionAbsent(t *testing.T) {
	for _, doc := range []string{"", "plain\ntext\n"} {
		sec, err := FindSection([]byte(doc))
		if err != nil || sec.Found || sec != (Section{}) {
			t.Errorf("%q: %+v, %v", doc, sec, err)
		}
	}
}

func TestFindSectionPresent(t *testing.T) {
	before, after := "intro\n\n", "\noutro\n"
	doc := before + BeginMarker + "\nline one\n\nline two\n" + EndMarker + "\n" + after
	sec, err := FindSection([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !sec.Found || sec.Start != len(before) || sec.End != len(doc)-len(after) || sec.Content != "line one\n\nline two" {
		t.Errorf("section = %+v", sec)
	}
}

func TestFindSectionEmptyAndAtEOF(t *testing.T) {
	doc := "a\n" + BeginMarker + "\n" + EndMarker
	sec, err := FindSection([]byte(doc))
	if err != nil || !sec.Found || sec.Content != "" || sec.Start != 2 || sec.End != len(doc) {
		t.Errorf("section = %+v, %v", sec, err)
	}
}

func TestFindSectionErrors(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"begin only", "a\n" + BeginMarker + "\nb\n", "line 2"},
		{"end only", "a\nb\n" + EndMarker + "\n", "line 3"},
		{"end before begin", EndMarker + "\nx\n" + BeginMarker + "\n", "line 1"},
		{"two pairs", block + "\n" + block, "line 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FindSection([]byte(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want mention of %q", err, tt.want)
			}
			if _, err := ReplaceSection([]byte(tt.doc), "x"); err == nil {
				t.Error("ReplaceSection accepted a malformed document")
			}
		})
	}
}

func TestMarkersMustBeWholeLine(t *testing.T) {
	doc := "Use `" + BeginMarker + "` and " + EndMarker + " in prose.\n" + BeginMarker + " trailing\n"
	sec, err := FindSection([]byte(doc))
	if err != nil || sec.Found {
		t.Errorf("section = %+v, %v", sec, err)
	}
}

func TestMarkersTolerateSpacesAndCR(t *testing.T) {
	doc := "  " + BeginMarker + "  \r\nbody\r\n\t" + EndMarker + "\r\n"
	sec, err := FindSection([]byte(doc))
	if err == nil {
		t.Fatalf("a tab-indented end marker is not a marker, got %+v", sec)
	}
	doc = "  " + BeginMarker + "  \r\nbody\r\n " + EndMarker + " \r\n"
	sec, err = FindSection([]byte(doc))
	if err != nil || !sec.Found || sec.Content != "body" || sec.End != len(doc) {
		t.Errorf("section = %+v, %v", sec, err)
	}
}

func TestSectionHashNormalizes(t *testing.T) {
	base := SectionHash("a\nb")
	for _, same := range []string{"a\r\nb", "a  \nb\t", "\n\na\nb\n\n", "\r\na \r\nb\r\n"} {
		if SectionHash(same) != base {
			t.Errorf("%q hashes differently", same)
		}
	}
	for _, diff := range []string{"a\nc", "a\n\nb", " a\nb"} {
		if SectionHash(diff) == base {
			t.Errorf("%q hashes the same", diff)
		}
	}
	if len(base) != 64 || base != strings.ToLower(base) {
		t.Errorf("hash = %q", base)
	}
}

func replace(t *testing.T, doc, content string) string {
	t.Helper()
	out, err := ReplaceSection([]byte(doc), content)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestReplaceSectionAppendsToExisting(t *testing.T) {
	doc := "# Mine\n\nrules\n"
	got := replace(t, doc, "body")
	if got != doc+"\n"+BeginMarker+"\nbody\n"+EndMarker+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionToEmptyDocument(t *testing.T) {
	if got := replace(t, "", "body"); got != block {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionNoTrailingNewline(t *testing.T) {
	if got := replace(t, "text", "body"); got != "text\n\n"+block {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionReplacesBetweenMarkers(t *testing.T) {
	before, after := "before\n\n", "\nafter no newline"
	doc := before + BeginMarker + "\nold\nold2\n" + EndMarker + "\n" + after
	got := replace(t, doc, "new")
	if got != before+BeginMarker+"\nnew\n"+EndMarker+"\n"+after {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionEmptyBodyAndContent(t *testing.T) {
	if got := replace(t, BeginMarker+"\n"+EndMarker+"\n", "x"); got != BeginMarker+"\nx\n"+EndMarker+"\n" {
		t.Errorf("got %q", got)
	}
	if got := replace(t, block, ""); got != BeginMarker+"\n"+EndMarker+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionIdempotent(t *testing.T) {
	doc := []byte("x\n\n" + BeginMarker + "\r\nbody  \r\n" + EndMarker + "\n")
	out, err := ReplaceSection(doc, "\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(doc) || &out[0] != &doc[0] {
		t.Errorf("document changed: %q", out)
	}
	first := replace(t, "x\n", "body")
	if replace(t, first, "body") != first {
		t.Error("second application changed the document")
	}
}

func TestReplaceSectionCRLFDocument(t *testing.T) {
	got := replace(t, "a\r\nb\r\n", "one\ntwo")
	want := "a\r\nb\r\n\r\n" + BeginMarker + "\r\none\r\ntwo\r\n" + EndMarker + "\r\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = replace(t, "a\n", "one")
	if strings.Contains(got, "\r") {
		t.Errorf("LF document got CR: %q", got)
	}
	got = replace(t, BeginMarker+"\r\nold\r\n"+EndMarker+"\r\n", "new")
	if got != BeginMarker+"\r\nnew\r\n"+EndMarker+"\r\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceSectionPreservesUserTextAroundBlock(t *testing.T) {
	pre := "# Title\n\nMention " + BeginMarker + " inline.\n\n"
	post := "\n## Mine\n  indented \ttext  \n\n\n"
	doc := pre + block + post
	got := replace(t, doc, "changed")
	if !strings.HasPrefix(got, pre) || !strings.HasSuffix(got, post) {
		t.Errorf("user text disturbed: %q", got)
	}
}
