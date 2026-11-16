package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGetPathFoundAndMissing(t *testing.T) {
	doc := []byte(`{"a":{"b":{"c":[1, 2]}},"x":1}`)
	v, ok, err := GetPath(doc, []string{"a", "b", "c"})
	if err != nil || !ok || string(v) != `[1,2]` {
		t.Fatalf("got %s %v %v", v, ok, err)
	}
	for _, p := range [][]string{{"a", "z"}, {"x", "y"}, {"q"}} {
		if _, ok, err := GetPath(doc, p); ok || err != nil {
			t.Errorf("%v: ok=%v err=%v", p, ok, err)
		}
	}
}

func TestGetPathInvalidJSONHasPosition(t *testing.T) {
	_, _, err := GetPath([]byte("{\n  \"a\": 1,\n  oops\n}"), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "line 3, column") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetPathEmptyDocument(t *testing.T) {
	for _, d := range []string{"", " \n\t"} {
		if _, ok, err := GetPath([]byte(d), []string{"a"}); ok || err != nil {
			t.Errorf("%q: ok=%v err=%v", d, ok, err)
		}
	}
}

func TestSetPathCreatesNested(t *testing.T) {
	out, err := SetPath(nil, []string{"a", "b"}, json.RawMessage(`{"k":1}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": {\n    \"b\": {\n      \"k\": 1\n    }\n  }\n}\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestSetPathPreservesOtherKeys(t *testing.T) {
	doc := []byte(`{"z":[1,{"q":true}],"a":{"keep":"me"}}`)
	out, err := SetPath(doc, []string{"a", "new"}, json.RawMessage(`2`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": {\n    \"keep\": \"me\",\n    \"new\": 2\n  },\n  \"z\": [\n    1,\n    {\n      \"q\": true\n    }\n  ]\n}\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestSetPathUnchangedReturnsOriginalBytes(t *testing.T) {
	doc := []byte("{ \"b\":1,\n\n \"a\" :   {\"y\":2,   \"x\":[1,2]} }")
	out, err := SetPath(doc, []string{"a"}, json.RawMessage(`{"x":[1,2],"y":2}`))
	if err != nil || !bytes.Equal(out, doc) {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestSetPathReplacesDifferentValue(t *testing.T) {
	out, err := SetPath([]byte(`{"a":1}`), []string{"a"}, json.RawMessage(`"x"`))
	if err != nil || string(out) != "{\n  \"a\": \"x\"\n}\n" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestSetPathNonObjectInTheWay(t *testing.T) {
	doc := []byte(`{"a":{"b":5}}`)
	out, err := SetPath(doc, []string{"a", "b", "c"}, json.RawMessage(`1`))
	if err == nil || !strings.Contains(err.Error(), "a.b.c") {
		t.Fatalf("err = %v", err)
	}
	if out != nil || string(doc) != `{"a":{"b":5}}` {
		t.Errorf("out=%q doc=%q", out, doc)
	}
}

func TestSetPathKeepsBigNumbers(t *testing.T) {
	doc := []byte(`{"big":12345678901234567890,"f":1.0,"e":1E5}`)
	out, err := SetPath(doc, []string{"n"}, json.RawMessage(`3`))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"12345678901234567890", "1.0", "1E5"} {
		if !strings.Contains(string(out), s) {
			t.Errorf("%s lost in %s", s, out)
		}
	}
}

func TestSetPathNoHTMLEscaping(t *testing.T) {
	out, err := SetPath(nil, []string{"a"}, json.RawMessage(`"<x> & y"`))
	if err != nil || !strings.Contains(string(out), `"<x> & y"`) {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestEnsureArrayItemAppends(t *testing.T) {
	out, added, err := EnsureArrayItem([]byte(`{"p":{"allow":["a"]}}`), []string{"p", "allow"}, json.RawMessage(`"b"`))
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	want := "{\n  \"p\": {\n    \"allow\": [\n      \"a\",\n      \"b\"\n    ]\n  }\n}\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestEnsureArrayItemAlreadyPresentUnchanged(t *testing.T) {
	doc := []byte(`{ "p":{"allow":[ "a" ,"b"]} }`)
	out, added, err := EnsureArrayItem(doc, []string{"p", "allow"}, json.RawMessage(`"b"`))
	if err != nil || added || !bytes.Equal(out, doc) {
		t.Fatalf("out=%q added=%v err=%v", out, added, err)
	}
}

func TestEnsureArrayItemCreatesArray(t *testing.T) {
	out, added, err := EnsureArrayItem(nil, []string{"p", "allow"}, json.RawMessage(`"b"`))
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	if string(out) != "{\n  \"p\": {\n    \"allow\": [\n      \"b\"\n    ]\n  }\n}\n" {
		t.Fatalf("got %q", out)
	}
}

func TestEnsureArrayItemNonArrayError(t *testing.T) {
	_, _, err := EnsureArrayItem([]byte(`{"p":{"allow":"x"}}`), []string{"p", "allow"}, json.RawMessage(`"b"`))
	if err == nil || !strings.Contains(err.Error(), "p.allow") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureArrayItemObjectItemsCanonicalEquality(t *testing.T) {
	doc := []byte(`{"l":[{"b":1,"a":2}]}`)
	out, added, err := EnsureArrayItem(doc, []string{"l"}, json.RawMessage(`{ "a":2, "b":1 }`))
	if err != nil || added || !bytes.Equal(out, doc) {
		t.Fatalf("out=%q added=%v err=%v", out, added, err)
	}
}

func TestHasArrayItem(t *testing.T) {
	doc := []byte(`{"l":["a",{"x":1}],"s":"str"}`)
	for item, want := range map[string]bool{`"a"`: true, `{"x":1}`: true, `"b"`: false} {
		got, err := HasArrayItem(doc, []string{"l"}, json.RawMessage(item))
		if err != nil || got != want {
			t.Errorf("%s: got %v %v", item, got, err)
		}
	}
	if got, err := HasArrayItem(doc, []string{"none"}, json.RawMessage(`"a"`)); got || err != nil {
		t.Errorf("missing: %v %v", got, err)
	}
	if _, err := HasArrayItem(doc, []string{"s"}, json.RawMessage(`"a"`)); err == nil {
		t.Error("non-array: no error")
	}
}

func TestCanonicalHashKeyOrderIndependent(t *testing.T) {
	a := CanonicalHash(json.RawMessage(`{"a":1,"b":{"y":[1,2],"x":2}}`))
	b := CanonicalHash(json.RawMessage(` { "b" : {"x":2,"y":[1,2]}, "a":1 } `))
	if a != b || len(a) != 64 || a != strings.ToLower(a) {
		t.Fatalf("%s %s", a, b)
	}
}

func TestCanonicalHashDiffers(t *testing.T) {
	if CanonicalHash(json.RawMessage(`{"a":1}`)) == CanonicalHash(json.RawMessage(`{"a":2}`)) {
		t.Fatal("equal hashes")
	}
}
