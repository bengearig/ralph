package embed

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeModelRemovesLatestOnly(t *testing.T) {
	for in, want := range map[string]string{
		"nomic-embed-text:latest": "nomic-embed-text",
		"nomic-embed-text":        "nomic-embed-text",
		"m:v2":                    "m:v2",
	} {
		if got := NormalizeModel(in); got != want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewFingerprintUsesBuiltInPrefixes(t *testing.T) {
	fp := NewFingerprint(Settings{Provider: "ollama", Model: "nomic-embed-text:latest", ChunkChars: 900}, Info{Digest: "abc", Dimensions: 768})
	want := Fingerprint{Provider: "ollama", Model: "nomic-embed-text", Digest: "abc", Dimensions: 768,
		DocumentPrefix: "search_document: ", QueryPrefix: "search_query: ", ChunkChars: 900}
	if fp != want {
		t.Errorf("fingerprint = %+v, want %+v", fp, want)
	}
}

func TestNewFingerprintEmptyAndHandWrittenPrefixAreEqual(t *testing.T) {
	a := NewFingerprint(Settings{Provider: "ollama", Model: "nomic-embed-text", ChunkChars: 1}, Info{})
	b := NewFingerprint(Settings{Provider: "ollama", Model: "nomic-embed-text", ChunkChars: 1,
		DocumentPrefix: "search_document: ", QueryPrefix: "search_query: "}, Info{})
	if a != b {
		t.Errorf("%+v != %+v", a, b)
	}
}

func TestNewFingerprintNonePrefix(t *testing.T) {
	fp := NewFingerprint(Settings{Provider: "ollama", Model: "nomic-embed-text", DocumentPrefix: "none", QueryPrefix: "none"}, Info{})
	if fp.DocumentPrefix != "" || fp.QueryPrefix != "" {
		t.Errorf("prefixes = %q, %q", fp.DocumentPrefix, fp.QueryPrefix)
	}
}

func TestNewFingerprintIgnoresURLAndTimeout(t *testing.T) {
	a := NewFingerprint(Settings{Provider: "ollama", Model: "m", URL: "http://a", Timeout: time.Second}, Info{})
	b := NewFingerprint(Settings{Provider: "ollama", Model: "m", URL: "http://b", Timeout: time.Hour}, Info{})
	if a != b {
		t.Errorf("%+v != %+v", a, b)
	}
}

func TestDifferencesEqualIsNil(t *testing.T) {
	fp := Fingerprint{Provider: "ollama", Model: "m", Digest: "d", Dimensions: 3, DocumentPrefix: "x", ChunkChars: 5}
	if got := Differences(fp, fp); got != nil {
		t.Errorf("Differences = %v", got)
	}
}

func TestDifferencesOrderAndText(t *testing.T) {
	a := Fingerprint{Provider: "ollama", Model: "m1", Digest: "aaaaaaaaaaaaaaaa", Dimensions: 3, DocumentPrefix: "d1", QueryPrefix: "q1", ChunkChars: 100}
	b := Fingerprint{Provider: "other", Model: "m2", Digest: "bbbbbbbbbbbbbbbb", Dimensions: 4, DocumentPrefix: "d2", QueryPrefix: "q2", ChunkChars: 200}
	want := []string{
		"provider ollama -> other",
		"model m1 -> m2",
		"digest aaaaaaaaaaaa -> bbbbbbbbbbbb",
		"dimensions 3 -> 4",
		`document prefix "d1" -> "d2"`,
		`query prefix "q1" -> "q2"`,
		"chunk size 100 -> 200",
	}
	if got := Differences(a, b); !reflect.DeepEqual(got, want) {
		t.Errorf("Differences = %q, want %q", got, want)
	}
}

func TestDifferencesDigestShortened(t *testing.T) {
	got := Differences(Fingerprint{Digest: "short"}, Fingerprint{Digest: "0123456789abcdef"})
	if want := []string{"digest short -> 0123456789ab"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Differences = %q, want %q", got, want)
	}
}

func TestDifferencesDigestOnlyWhenBothSet(t *testing.T) {
	if got := Differences(Fingerprint{Digest: "a"}, Fingerprint{}); got != nil {
		t.Errorf("Differences = %q", got)
	}
	if got := Differences(Fingerprint{}, Fingerprint{Digest: "a"}); got != nil {
		t.Errorf("Differences = %q", got)
	}
}

func TestDifferencesDimensionsOnlyWhenBothSet(t *testing.T) {
	if got := Differences(Fingerprint{Dimensions: 3}, Fingerprint{}); got != nil {
		t.Errorf("Differences = %q", got)
	}
	if got := Differences(Fingerprint{}, Fingerprint{Dimensions: 3}); got != nil {
		t.Errorf("Differences = %q", got)
	}
}

func TestDifferencesModelLatestTag(t *testing.T) {
	a := NewFingerprint(Settings{Provider: "ollama", Model: "m:latest"}, Info{})
	b := NewFingerprint(Settings{Provider: "ollama", Model: "m"}, Info{})
	if got := Differences(a, b); got != nil {
		t.Errorf("Differences = %q", got)
	}
}

func TestFingerprintJSONRoundTrip(t *testing.T) {
	for _, fp := range []Fingerprint{
		{Provider: "ollama", Model: "m", Digest: "d", Dimensions: 3, DocumentPrefix: "x", QueryPrefix: "y", ChunkChars: 5},
		{Provider: "ollama", Model: "m"},
	} {
		b, err := json.Marshal(fp)
		if err != nil {
			t.Fatal(err)
		}
		var got Fingerprint
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got != fp {
			t.Errorf("round trip %s: %+v != %+v", b, got, fp)
		}
	}
}
