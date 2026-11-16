package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func parsed(t *testing.T, in string) string {
	t.Helper()
	out, err := ParseJSONC([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("not valid json: %q", out)
	}
	return string(out)
}

func canon(t *testing.T, s string) string {
	t.Helper()
	b, err := canonicalBytes(json.RawMessage(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseJSONCLineComment(t *testing.T) {
	got := parsed(t, "{\n// note\n\"a\": 1 // tail\n}")
	if canon(t, got) != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCBlockComment(t *testing.T) {
	got := parsed(t, "{/* one\ntwo */\"a\":/* x */1}")
	if canon(t, got) != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCCommentAtEndWithoutNewline(t *testing.T) {
	got := parsed(t, `{"a":1} // end`)
	if canon(t, got) != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCStringsAreNotComments(t *testing.T) {
	in := `{"u":"http://example.com","c":"/* x */","d":"// y"}`
	if got := parsed(t, in); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCEscapedQuoteInString(t *testing.T) {
	in := `{"a":"say \"hi\" // not a comment","b":"back\\"} // real`
	got := parsed(t, in)
	if canon(t, got) != `{"a":"say \"hi\" // not a comment","b":"back\\"}` {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCTrailingCommas(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1,}`:                       `{"a":1}`,
		`[1,2,]`:                         `[1,2]`,
		`{"a":[1,{"b":2,},],}`:           `{"a":[1,{"b":2}]}`,
		"{\"a\":[1, // c\n/* d */ ]\n,}": `{"a":[1]}`,
		`{"a":",}","b":[","]}`:           `{"a":",}","b":[","]}`,
	} {
		if got := canon(t, parsed(t, in)); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestParseJSONCByteOrderMark(t *testing.T) {
	if got := canon(t, parsed(t, "\xef\xbb\xbf{\"a\":1}")); got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestParseJSONCUnterminatedBlockCommentPosition(t *testing.T) {
	_, err := ParseJSONC([]byte("{\n  \"a\": 1, /* open\n}"))
	if err == nil || !strings.Contains(err.Error(), "line 2, column 11") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseJSONCUnterminatedStringPosition(t *testing.T) {
	_, err := ParseJSONC([]byte("{\n  \"a\": \"open}"))
	if err == nil || !strings.Contains(err.Error(), "line 2, column 8") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseJSONCPlainJSONUnchangedSemantically(t *testing.T) {
	in := "{\n  \"a\": [1, 2.5, true, null, {\"b\": \"c\"}],\n  \"d\": \"e\"\n}\n"
	if canon(t, parsed(t, in)) != canon(t, in) {
		t.Fatal("changed")
	}
}

func TestGetPathJSONCFoundAndMissing(t *testing.T) {
	doc := []byte("{\n// c\n\"a\": {\"b\": [1,],},\n}")
	v, ok, err := GetPathJSONC(doc, []string{"a", "b"})
	if err != nil || !ok || string(v) != "[1]" {
		t.Fatalf("v=%s ok=%v err=%v", v, ok, err)
	}
	if _, ok, err := GetPathJSONC(doc, []string{"a", "x"}); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, _, err := GetPathJSONC([]byte("{/*"), []string{"a"}); err == nil {
		t.Fatal("want error")
	}
	has, err := HasArrayItemJSONC(doc, []string{"a", "b"}, json.RawMessage("1"))
	if err != nil || !has {
		t.Fatalf("has=%v err=%v", has, err)
	}
}
