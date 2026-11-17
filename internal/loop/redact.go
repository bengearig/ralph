package loop

import (
	"bytes"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/bengearig/ralph/internal/config"
)

const (
	redacted          = "[redacted]"
	minSecretLength   = 8
	maxPendingLineLen = 1 << 20
)

// SecretValues returns the current values of the registered secret settings that resolve.
func SecretValues(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range config.DefaultRegistry().Settings() {
		if s.Kind != config.KindSecret {
			continue
		}
		v, err := cfg.Secret(s.Path)
		if err != nil || v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

type redactor struct {
	r      *strings.Replacer
	maxLen int
}

func newRedactor(secrets []string) redactor {
	var vals []string
	seen := map[string]bool{}
	for _, s := range secrets {
		if len(s) >= minSecretLength && !seen[s] {
			seen[s] = true
			vals = append(vals, s)
		}
	}
	if len(vals) == 0 {
		return redactor{}
	}
	sort.SliceStable(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, redacted)
	}
	return redactor{r: strings.NewReplacer(pairs...), maxLen: len(vals[0])}
}

func (r redactor) String(s string) string {
	if r.r == nil {
		return s
	}
	return r.r.Replace(s)
}

func (r redactor) Strings(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.String(s)
	}
	return out
}

// lineWriter redacts what it receives a line at a time, so a value split
// across writes is still caught.
type lineWriter struct {
	mu  sync.Mutex
	red redactor
	dst io.Writer
	buf []byte
	err error
}

func newLineWriter(red redactor, dst io.Writer) *lineWriter {
	return &lineWriter{red: red, dst: dst}
}

// Write never fails, so a full disk cannot disturb the harness; the first error is kept for Close.
func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.red.String(string(w.buf[:i+1])))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxPendingLineLen {
		red := w.red.String(string(w.buf))
		keep := w.red.maxLen
		if keep > len(red) {
			keep = len(red)
		}
		w.emit(red[:len(red)-keep])
		w.buf = []byte(red[len(red)-keep:])
	}
	return len(p), nil
}

// Close flushes the unfinished line and returns the first write error.
func (w *lineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.red.String(string(w.buf)))
		w.buf = nil
	}
	return w.err
}

func (w *lineWriter) emit(s string) {
	if s == "" || w.err != nil {
		return
	}
	_, w.err = io.WriteString(w.dst, s)
}
