package search

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseQueryTable(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"words", "dark mode toggle", []string{"dark", "mode", "toggle"}},
		{"mixed case kept", "Dark MODE", []string{"Dark", "MODE"}},
		{"punctuation and hyphens", "dark-mode, toggle!", []string{"dark", "mode", "toggle"}},
		{"underscore splits", "unit_get", []string{"unit", "get"}},
		{"phrase", `a "mode toggle" b`, []string{"a", "mode toggle", "b"}},
		{"unterminated quote", `a "mode toggle`, []string{"a", "mode toggle"}},
		{"unicode letters", "café naïve 日本語", []string{"café", "naïve", "日本語"}},
		{"digits", "v2 100", []string{"v2", "100"}},
		{"leading and trailing spaces", "  hello  ", []string{"hello"}},
		{"empty phrase dropped", `"" a "--"`, []string{"a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParseQuery(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.Terms, c.want) {
				t.Errorf("terms = %q, want %q", p.Terms, c.want)
			}
		})
	}
}

func TestParseQueryEmptyIsInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "!!! --- ???", `""`} {
		if _, err := ParseQuery(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestParseQueryTooManyTerms(t *testing.T) {
	words := func(n int) string {
		w := make([]string, n)
		for i := range w {
			w[i] = fmt.Sprintf("w%d", i)
		}
		return strings.Join(w, " ")
	}
	if p, err := ParseQuery(words(20)); err != nil || len(p.Terms) != 20 {
		t.Errorf("20 terms: %v, %v", p.Terms, err)
	}
	if _, err := ParseQuery(words(21)); !errors.Is(err, ErrInvalid) {
		t.Errorf("21 terms: err = %v, want ErrInvalid", err)
	}
}

func TestMatchExpressionQuoting(t *testing.T) {
	if got := (Parsed{Terms: []string{`a"b`}}).Match("AND"); got != `"a""b"` {
		t.Errorf("got %s", got)
	}
	p, err := ParseQuery(`dark "mode toggle" x`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Match("AND"), `"dark" AND "mode toggle" AND "x"`; got != want {
		t.Errorf("AND = %s, want %s", got, want)
	}
	if got, want := p.Match("OR"), `"dark" OR "mode toggle" OR "x"`; got != want {
		t.Errorf("OR = %s, want %s", got, want)
	}
}
