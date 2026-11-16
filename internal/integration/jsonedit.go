package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func parseDoc(doc []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return map[string]any{}, nil
	}
	v, err := decode(doc)
	if err != nil {
		return nil, positioned(doc, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("json document is not an object")
	}
	return obj, nil
}

func decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the top-level value")
		}
		return nil, err
	}
	return v, nil
}

func positioned(doc []byte, err error) error {
	var off int64 = -1
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	case errors.Is(err, io.ErrUnexpectedEOF):
		off = int64(len(doc))
	}
	if off < 0 {
		return fmt.Errorf("invalid json: %w", err)
	}
	if off > int64(len(doc)) {
		off = int64(len(doc))
	}
	prefix := doc[:off]
	line := bytes.Count(prefix, []byte("\n")) + 1
	col := int(off) - (bytes.LastIndexByte(prefix, '\n') + 1)
	if col < 1 {
		col = 1
	}
	return fmt.Errorf("invalid json at line %d, column %d: %w", line, col, err)
}

func compact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func render(obj map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func parseValue(value json.RawMessage) (any, error) {
	v, err := decode(value)
	if err != nil {
		return nil, fmt.Errorf("invalid json value: %w", err)
	}
	return v, nil
}

func canonicalBytes(value json.RawMessage) ([]byte, error) {
	v, err := parseValue(value)
	if err != nil {
		return nil, err
	}
	return compact(v)
}

func lookup(obj map[string]any, keyPath []string) (any, bool) {
	var cur any = obj
	for _, k := range keyPath {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func pathName(keyPath []string) string { return strings.Join(keyPath, ".") }

// GetPath returns the compact JSON of the value at keyPath.
func GetPath(doc []byte, keyPath []string) (json.RawMessage, bool, error) {
	obj, err := parseDoc(doc)
	if err != nil {
		return nil, false, err
	}
	v, ok := lookup(obj, keyPath)
	if !ok {
		return nil, false, nil
	}
	b, err := compact(v)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// parent returns the object holding the last key of keyPath, creating
// missing objects on the way.
func parent(obj map[string]any, keyPath []string) (map[string]any, error) {
	cur := obj
	for i, k := range keyPath[:len(keyPath)-1] {
		next, ok := cur[k]
		if !ok {
			m := map[string]any{}
			cur[k] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("key path %q: %q is not an object", pathName(keyPath), pathName(keyPath[:i+1]))
		}
		cur = m
	}
	return cur, nil
}

// SetPath sets the value at keyPath, leaving the document byte-for-byte
// unchanged when it already holds an equal value.
func SetPath(doc []byte, keyPath []string, value json.RawMessage) ([]byte, error) {
	if len(keyPath) == 0 {
		return nil, errors.New("empty key path")
	}
	nv, err := parseValue(value)
	if err != nil {
		return nil, err
	}
	obj, err := parseDoc(doc)
	if err != nil {
		return nil, err
	}
	if cur, ok := lookup(obj, keyPath); ok {
		a, err := compact(cur)
		if err != nil {
			return nil, err
		}
		b, err := compact(nv)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(a, b) {
			return doc, nil
		}
	}
	p, err := parent(obj, keyPath)
	if err != nil {
		return nil, err
	}
	p[keyPath[len(keyPath)-1]] = nv
	return render(obj)
}

// EnsureArrayItem appends item to the array at keyPath unless an equal
// element is present.
func EnsureArrayItem(doc []byte, keyPath []string, item json.RawMessage) ([]byte, bool, error) {
	if len(keyPath) == 0 {
		return nil, false, errors.New("empty key path")
	}
	ni, err := parseValue(item)
	if err != nil {
		return nil, false, err
	}
	obj, err := parseDoc(doc)
	if err != nil {
		return nil, false, err
	}
	want, err := compact(ni)
	if err != nil {
		return nil, false, err
	}
	var arr []any
	if cur, ok := lookup(obj, keyPath); ok {
		var isArr bool
		if arr, isArr = cur.([]any); !isArr {
			return nil, false, fmt.Errorf("key path %q: value is not an array", pathName(keyPath))
		}
		for _, el := range arr {
			got, err := compact(el)
			if err != nil {
				return nil, false, err
			}
			if bytes.Equal(got, want) {
				return doc, false, nil
			}
		}
	}
	p, err := parent(obj, keyPath)
	if err != nil {
		return nil, false, err
	}
	p[keyPath[len(keyPath)-1]] = append(arr, ni)
	out, err := render(obj)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// HasArrayItem reports whether the array at keyPath holds an element equal
// to item. A missing array holds nothing.
func HasArrayItem(doc []byte, keyPath []string, item json.RawMessage) (bool, error) {
	ni, err := parseValue(item)
	if err != nil {
		return false, err
	}
	obj, err := parseDoc(doc)
	if err != nil {
		return false, err
	}
	cur, ok := lookup(obj, keyPath)
	if !ok {
		return false, nil
	}
	arr, ok := cur.([]any)
	if !ok {
		return false, fmt.Errorf("key path %q: value is not an array", pathName(keyPath))
	}
	want, err := compact(ni)
	if err != nil {
		return false, err
	}
	for _, el := range arr {
		got, err := compact(el)
		if err != nil {
			return false, err
		}
		if bytes.Equal(got, want) {
			return true, nil
		}
	}
	return false, nil
}

// CanonicalHash is the lowercase hex SHA-256 of the value as compact JSON
// with object keys sorted at every level. Invalid JSON hashes as written.
func CanonicalHash(value json.RawMessage) string {
	b, err := canonicalBytes(value)
	if err != nil {
		b = value
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
