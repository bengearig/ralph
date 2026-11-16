package verify

import (
	"strings"
	"testing"
)

func TestTailShortKept(t *testing.T) {
	in := "a\nb\n" + strings.Repeat("x", maxOutput-4)
	if got := tail([]byte(in)); got != in {
		t.Fatalf("short output changed: %d bytes", len(got))
	}
	if got := tail(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
}

func TestTailCutsAtLineBoundary(t *testing.T) {
	line := strings.Repeat("y", 99) + "\n"
	in := strings.Repeat(line, 100)
	got := tail([]byte(in))
	if !strings.HasPrefix(got, "[output cut]\n"+line) {
		t.Fatalf("prefix = %q", got[:40])
	}
	body := strings.TrimPrefix(got, "[output cut]\n")
	if len(body) > maxOutput || !strings.HasSuffix(in, body) || len(body)%100 != 0 {
		t.Fatalf("body of %d bytes is not whole trailing lines", len(body))
	}
}

func TestTailNoNewlineKeepsWindow(t *testing.T) {
	in := strings.Repeat("z", maxOutput+500)
	got := tail([]byte(in))
	if got != "[output cut]\n"+strings.Repeat("z", maxOutput) {
		t.Fatalf("got %d bytes", len(got))
	}
}

func TestTailDropsInvalidUTF8Prefix(t *testing.T) {
	in := strings.Repeat("é", maxOutput)
	got := strings.TrimPrefix(tail([]byte(in)), "[output cut]\n")
	if got == "" || strings.ContainsRune(got, '�') || !strings.HasPrefix(got, "é") {
		t.Fatalf("bad prefix %q", got[:4])
	}
	if len(got) != maxOutput {
		t.Fatalf("len = %d", len(got))
	}
}

func TestReportFailedReturnsFirst(t *testing.T) {
	var empty Report
	if !empty.Passed() {
		t.Fatal("empty report should pass")
	}
	if _, ok := empty.Failed(); ok {
		t.Fatal("empty report has no failure")
	}
	r := Report{Results: []Result{
		{Command: "a"},
		{Command: "b", ExitCode: 2},
		{Command: "c", TimedOut: true, ExitCode: -1},
	}}
	if r.Passed() {
		t.Fatal("report with failures passed")
	}
	if f, ok := r.Failed(); !ok || f.Command != "b" {
		t.Fatalf("failed = %+v, %v", f, ok)
	}
	if (Result{StartErr: "x"}).Passed() {
		t.Fatal("start error passed")
	}
}
