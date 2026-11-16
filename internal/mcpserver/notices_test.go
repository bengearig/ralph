package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func noticeServer(t *testing.T, sources ...NoticeSource) *mcp.ClientSession {
	t.Helper()
	return connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t)), Sources: sources})
}

func fixed(ns ...Notice) NoticeSource {
	return func(context.Context) ([]Notice, error) { return ns, nil }
}

func structuredNotices(t *testing.T, res *mcp.CallToolResult) []Notice {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var sc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	field, ok := sc["notices"]
	if !ok {
		t.Fatalf("no notices field in %s", raw)
	}
	if string(field) == "null" {
		t.Fatalf("notices is null in %s", raw)
	}
	var ns []Notice
	if err := json.Unmarshal(field, &ns); err != nil {
		t.Fatal(err)
	}
	return ns
}

func lastText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := res.Content[len(res.Content)-1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("last content is %T", res.Content[len(res.Content)-1])
	}
	return tc.Text
}

func callMissingUnit(t *testing.T, cs *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "unit_get", Arguments: map[string]any{"id": "T-999"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestNoticesEmptyByDefault(t *testing.T) {
	cs := noticeServer(t)
	res := callStatus(t, cs, nil)
	if ns := structuredNotices(t, res); len(ns) != 0 {
		t.Errorf("notices = %v", ns)
	}
	if len(res.Content) != 1 {
		t.Errorf("content items = %d, want 1", len(res.Content))
	}
}

func TestNoticeAttachedAsStructuredAndText(t *testing.T) {
	cs := noticeServer(t, fixed(Notice{Code: "stale", Message: "index is stale"}))
	res := callStatus(t, cs, nil)
	if ns := structuredNotices(t, res); !reflect.DeepEqual(ns, []Notice{{Code: "stale", Message: "index is stale"}}) {
		t.Errorf("notices = %v", ns)
	}
	if got := lastText(t, res); got != "Notice for the user: index is stale" {
		t.Errorf("last text = %q", got)
	}
}

func TestNoticeOnceNeverRepeated(t *testing.T) {
	cs := noticeServer(t, fixed(Notice{Code: "a", Message: "m"}))
	for i := 0; i < 3; i++ {
		res := callStatus(t, cs, nil)
		want := 0
		if i == 0 {
			want = 1
		}
		if ns := structuredNotices(t, res); len(ns) != want {
			t.Errorf("call %d: notices = %v", i, ns)
		}
		if strings.Contains(text(res), "Notice for the user") != (want == 1) {
			t.Errorf("call %d: text = %q", i, text(res))
		}
	}
}

func TestNoticeChangedMessageRepeats(t *testing.T) {
	var n atomic.Int32
	src := func(context.Context) ([]Notice, error) {
		return []Notice{{Code: "c", Message: fmt.Sprintf("m%d", min(n.Add(1), 2))}}, nil
	}
	cs := noticeServer(t, src)
	for i, want := range []int{1, 1, 0} {
		if ns := structuredNotices(t, callStatus(t, cs, nil)); len(ns) != want {
			t.Errorf("call %d: notices = %v, want %d", i, ns, want)
		}
	}
}

func TestNoticeOnErrorResults(t *testing.T) {
	cs := noticeServer(t, fixed(Notice{Code: "a", Message: "m"}))
	res := callMissingUnit(t, cs)
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if code := errorCode(t, res); code != codeNotFound {
		t.Errorf("error_code = %q", code)
	}
	if ns := structuredNotices(t, res); len(ns) != 1 {
		t.Errorf("notices = %v", ns)
	}
	if got := lastText(t, res); got != "Notice for the user: m" {
		t.Errorf("last text = %q", got)
	}
}

func captureLogs(t *testing.T) (log, stdout *bytes.Buffer) {
	t.Helper()
	log = &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	stdout = &bytes.Buffer{}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(stdout, r); close(done) }()
	t.Cleanup(func() {
		os.Stdout = orig
		_ = w.Close()
		<-done
		if stdout.Len() != 0 {
			t.Errorf("wrote to stdout: %q", stdout.String())
		}
	})
	return log, stdout
}

func TestNoticeSourceErrorSkipped(t *testing.T) {
	logs, _ := captureLogs(t)
	bad := func(context.Context) ([]Notice, error) { return nil, errors.New("boom") }
	cs := noticeServer(t, bad, fixed(Notice{Code: "ok", Message: "fine"}))
	want := text(callStatus(t, noticeServer(t), nil))
	res := callStatus(t, cs, nil)
	if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Code != "ok" {
		t.Errorf("notices = %v", ns)
	}
	if res.IsError || !strings.HasPrefix(text(res), want) {
		t.Errorf("result changed: %q", text(res))
	}
	if l := logs.String(); !strings.Contains(l, "level=WARN") || !strings.Contains(l, "boom") {
		t.Errorf("log = %q", l)
	}
}

func TestNoticeSourcePanicSkipped(t *testing.T) {
	logs, _ := captureLogs(t)
	bad := func(context.Context) ([]Notice, error) { panic("kaboom") }
	cs := noticeServer(t, bad, fixed(Notice{Code: "ok", Message: "fine"}))
	res := callStatus(t, cs, nil)
	if res.IsError {
		t.Fatalf("error result: %q", text(res))
	}
	if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Code != "ok" {
		t.Errorf("notices = %v", ns)
	}
	if !strings.Contains(logs.String(), "kaboom") {
		t.Errorf("log = %q", logs.String())
	}
}

func TestNoticeSourcesInOrder(t *testing.T) {
	cs := noticeServer(t,
		fixed(Notice{Code: "1", Message: "first"}),
		fixed(Notice{Code: "2", Message: "second"}, Notice{Code: "3", Message: "third"}),
	)
	res := callStatus(t, cs, nil)
	var got []string
	for _, n := range structuredNotices(t, res) {
		got = append(got, n.Message)
	}
	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(got, want) {
		t.Errorf("notices = %v", got)
	}
	n := len(res.Content)
	for i, m := range []string{"first", "second", "third"} {
		tc := res.Content[n-3+i].(*mcp.TextContent)
		if tc.Text != "Notice for the user: "+m {
			t.Errorf("text %d = %q", i, tc.Text)
		}
	}
}

func TestNoticeTruncated(t *testing.T) {
	cs := noticeServer(t, fixed(Notice{Code: "long", Message: strings.Repeat("x", 600)}))
	res := callStatus(t, cs, nil)
	ns := structuredNotices(t, res)
	if len(ns) != 1 {
		t.Fatalf("notices = %v", ns)
	}
	r := []rune(ns[0].Message)
	if len(r) != 500 || r[499] != '…' || strings.Trim(string(r[:499]), "x") != "" {
		t.Errorf("message length %d, tail %q", len(r), string(r[len(r)-3:]))
	}
	if got := lastText(t, res); got != "Notice for the user: "+ns[0].Message {
		t.Errorf("text not truncated: %d", len(got))
	}
}

func TestNoticesConcurrentCalls(t *testing.T) {
	var n atomic.Int32
	src := func(context.Context) ([]Notice, error) {
		i := n.Add(1)
		return []Notice{{Code: "c", Message: fmt.Sprintf("m%d", i)}}, nil
	}
	cs := noticeServer(t, src)
	const calls = 16
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "status"})
			if err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal(res.StructuredContent)
			var sc struct{ Notices []Notice }
			if err := json.Unmarshal(raw, &sc); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, x := range sc.Notices {
				seen[x.Message]++
			}
		}()
	}
	wg.Wait()
	if len(seen) != calls {
		t.Errorf("distinct notices = %d, want %d", len(seen), calls)
	}
	for m, c := range seen {
		if c != 1 {
			t.Errorf("%s attached %d times", m, c)
		}
	}
}

func TestNoticesInOutputSchemas(t *testing.T) {
	cs := noticeServer(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools")
	}
	for _, tool := range res.Tools {
		raw, _ := json.Marshal(tool.OutputSchema)
		var sc struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatal(err)
		}
		if _, ok := sc.Properties["notices"]; !ok {
			t.Errorf("%s: output schema has no notices property", tool.Name)
		}
	}
}

func TestUnitSourcesRunAfterServiceOpen(t *testing.T) {
	svc := seeded(t)
	var built, ran atomic.Int32
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(svc), UnitSources: []func(units.Service) NoticeSource{
		func(got units.Service) NoticeSource {
			built.Add(1)
			if got != svc {
				t.Errorf("source built over a different service")
			}
			return func(context.Context) ([]Notice, error) {
				n := ran.Add(1)
				return []Notice{{Code: "u", Message: fmt.Sprintf("run %d", n)}}, nil
			}
		},
	}})
	if built.Load() != 0 || ran.Load() != 0 {
		t.Fatalf("before any call: built %d ran %d", built.Load(), ran.Load())
	}
	for i := 1; i <= 2; i++ {
		res := callStatus(t, cs, nil)
		if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Message != fmt.Sprintf("run %d", i) {
			t.Errorf("call %d notices = %v", i, ns)
		}
	}
	if built.Load() != 1 || ran.Load() != 2 {
		t.Errorf("built %d ran %d", built.Load(), ran.Load())
	}
}

func TestUnitSourcesSkippedWhenOpenFails(t *testing.T) {
	var built, ran atomic.Int32
	cs := connect(t, Options{
		Name: "ralph",
		Open: func(context.Context) (units.Service, error) { return nil, errors.New("no store") },
		UnitSources: []func(units.Service) NoticeSource{func(units.Service) NoticeSource {
			built.Add(1)
			return func(context.Context) ([]Notice, error) { ran.Add(1); return nil, nil }
		}},
		Sources: []NoticeSource{fixed(Notice{Code: "ok", Message: "fine"})},
	})
	res := callStatus(t, cs, nil)
	if !res.IsError {
		t.Fatal("want an error result")
	}
	if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Code != "ok" {
		t.Errorf("notices = %v", ns)
	}
	if built.Load() != 0 || ran.Load() != 0 {
		t.Errorf("built %d ran %d", built.Load(), ran.Load())
	}
}

func TestUnitSourceFailureIsLoggedAndSkipped(t *testing.T) {
	logs, _ := captureLogs(t)
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t)), UnitSources: []func(units.Service) NoticeSource{
		func(units.Service) NoticeSource {
			return func(context.Context) ([]Notice, error) { return nil, errors.New("unit boom") }
		},
		func(units.Service) NoticeSource { return fixed(Notice{Code: "ok", Message: "fine"}) },
	}})
	res := callStatus(t, cs, nil)
	if res.IsError {
		t.Fatalf("error result: %q", text(res))
	}
	if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Code != "ok" {
		t.Errorf("notices = %v", ns)
	}
	if l := logs.String(); !strings.Contains(l, "level=WARN") || !strings.Contains(l, "unit boom") {
		t.Errorf("log = %q", l)
	}
}
