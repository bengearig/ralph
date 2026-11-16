package search

import (
	"fmt"
	"strings"
	"unicode"
)

const maxTerms = 20

const (
	joinAnd = "AND"
	joinOr  = "OR"
)

// Parsed is a query's terms: single words, or the text of a quoted phrase.
type Parsed struct {
	Terms []string
}

// ParseQuery splits text into terms. Runs of letters and digits are words and
// double-quoted segments are phrases; every other character separates. An
// unterminated quote makes the rest of the text a phrase.
func ParseQuery(text string) (Parsed, error) {
	var terms []string
	rs := []rune(text)
	for i := 0; i < len(rs); {
		switch r := rs[i]; {
		case r == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if p := string(rs[i+1 : j]); hasWord(p) {
				terms = append(terms, p)
			}
			i = j + 1
		case isWordRune(r):
			j := i
			for j < len(rs) && isWordRune(rs[j]) {
				j++
			}
			terms = append(terms, string(rs[i:j]))
			i = j
		default:
			i++
		}
	}
	if len(terms) == 0 {
		return Parsed{}, fmt.Errorf("%w: the query has no words", ErrInvalid)
	}
	if len(terms) > maxTerms {
		return Parsed{}, fmt.Errorf("%w: the query has %d terms, at most %d are allowed", ErrInvalid, len(terms), maxTerms)
	}
	return Parsed{Terms: terms}, nil
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func hasWord(s string) bool {
	return strings.IndexFunc(s, isWordRune) >= 0
}

// Match builds the FTS expression joining the terms with joiner, AND or OR.
// Each term is a quoted FTS string, so nothing in it is syntax.
func (p Parsed) Match(joiner string) string {
	parts := make([]string, len(p.Terms))
	for i, t := range p.Terms {
		parts[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(parts, " "+joiner+" ")
}
