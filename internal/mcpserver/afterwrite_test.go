package mcpserver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, n *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("calls = %d, want %d", n.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAfterWriteCalledForWritingTool(t *testing.T) {
	var n atomic.Int32
	svc := newSvc(t)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(svc), AfterWrite: func(context.Context) { n.Add(1) }})
	create(t, cs, map[string]any{"level": "brief", "title": "A", "body": "b"})
	waitFor(t, &n, 1)
}

func TestAfterWriteNotCalledForReadOnlyTool(t *testing.T) {
	var n atomic.Int32
	svc := newSvc(t)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(svc), AfterWrite: func(context.Context) { n.Add(1) }})
	ok(t, cs, "unit_list", map[string]any{})
	ok(t, cs, "status", map[string]any{})
	time.Sleep(100 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatalf("calls = %d, want 0", n.Load())
	}
}

func TestAfterWriteNotCalledWhenToolFails(t *testing.T) {
	var n atomic.Int32
	svc := newSvc(t)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(svc), AfterWrite: func(context.Context) { n.Add(1) }})
	res := call(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "S-99", "title": "A", "body": "b", "acceptance_criteria": "c"})
	if !res.IsError {
		t.Fatalf("want a refused write, got %s", text(res))
	}
	time.Sleep(100 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatalf("calls = %d, want 0", n.Load())
	}
}

func TestAfterWriteDoesNotDelayResult(t *testing.T) {
	var n atomic.Int32
	svc := newSvc(t)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(svc), AfterWrite: func(context.Context) {
		time.Sleep(time.Second)
		n.Add(1)
	}})
	start := time.Now()
	create(t, cs, map[string]any{"level": "brief", "title": "A", "body": "b"})
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("result took %v", d)
	}
	waitFor(t, &n, 1)
}

func TestAfterWriteNilIsFine(t *testing.T) {
	cs, _ := newClient(t)
	create(t, cs, map[string]any{"level": "brief", "title": "A", "body": "b"})
}
