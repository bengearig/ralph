package adf

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func tx(s string) string { return `{"type":"text","text":` + jq(s) + `}` }

func jq(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func marked(s string, marks ...string) string {
	return `{"type":"text","text":` + jq(s) + `,"marks":[` + strings.Join(marks, ",") + `]}`
}

func mk(t string) string { return `{"type":"` + t + `"}` }

func link(href string) string {
	return `{"type":"link","attrs":{"href":` + jq(href) + `}}`
}

func node(t string, children ...string) string {
	return `{"type":"` + t + `","content":[` + strings.Join(children, ",") + `]}`
}

func para(children ...string) string { return node("paragraph", children...) }

func heading(level int, children ...string) string {
	return `{"type":"heading","attrs":{"level":` + string(rune('0'+level)) + `},"content":[` + strings.Join(children, ",") + `]}`
}

func li(children ...string) string { return node("listItem", children...) }

func code(lang, text string) string {
	out := `{"type":"codeBlock"`
	if lang != "" {
		out += `,"attrs":{"language":` + jq(lang) + `}`
	}
	if text != "" {
		out += `,"content":[` + tx(text) + `]`
	}
	return out + "}"
}

func content(blocks ...string) string { return "[" + strings.Join(blocks, ",") + "]" }

func assertContent(t *testing.T, d Doc, want string) {
	t.Helper()
	gotRaw, err := json.Marshal(d.Content)
	if err != nil {
		t.Fatal(err)
	}
	var got, exp any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatalf("bad expectation %s: %v", want, err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("content mismatch\n got: %s\nwant: %s", gotRaw, want)
	}
}

type mdCase struct {
	name, in, want string
}

func runContentCases(t *testing.T, cases []mdCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertContent(t, FromMarkdown(c.in), c.want)
		})
	}
}

func equalJSON(a, b any) bool { return reflect.DeepEqual(a, b) }
