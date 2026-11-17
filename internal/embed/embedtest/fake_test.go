package embedtest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed/core"
)

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func TestFakeSharedWordsAreCloser(t *testing.T) {
	f := NewFake(0)
	v, err := f.Embed(context.Background(), []string{
		"the quick brown fox jumps", "a quick brown fox runs", "quarterly invoice payment schedule",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(v[0]) != 64 {
		t.Fatalf("default dimensions = %d", len(v[0]))
	}
	if related, unrelated := dot(v[0], v[1]), dot(v[0], v[2]); related <= unrelated {
		t.Errorf("related %v <= unrelated %v", related, unrelated)
	}
	if self := dot(v[0], v[0]); self < 0.999 || self > 1.001 {
		t.Errorf("self dot = %v", self)
	}
}

func TestFakeDeterministic(t *testing.T) {
	a, _ := NewFake(32).Embed(context.Background(), []string{"Hello, World 42"})
	b, _ := NewFake(32).Embed(context.Background(), []string{"hello world 42"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("vectors differ: %v vs %v", a, b)
	}
	if len(Vector("", 8)) != 8 {
		t.Error("empty text has no vector")
	}
}

func TestFakeFailAndRecover(t *testing.T) {
	f := NewFake(8)
	boom := errors.New("boom")
	f.FailWith(boom)
	if _, err := f.Embed(context.Background(), []string{"x"}); !errors.Is(err, boom) {
		t.Errorf("Embed err = %v", err)
	}
	if _, err := f.Describe(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Describe err = %v", err)
	}
	f.Recover()
	if _, err := f.Embed(context.Background(), []string{"x"}); err != nil {
		t.Errorf("after Recover: %v", err)
	}
	info, err := f.Describe(context.Background())
	if err != nil || info.Provider != "fake" || info.Model != "fake" || info.Digest != "fake" || info.Dimensions != 8 {
		t.Errorf("info = %+v, %v", info, err)
	}
}

func TestFakeDelayHonoursCancel(t *testing.T) {
	f := NewFake(8)
	f.SetDelay(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := f.Embed(ctx, []string{"x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("did not return on cancel")
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := NewFake(8)
	_, _ = f.Embed(context.Background(), []string{"a", "b"})
	_, _ = f.Embed(context.Background(), []string{"c"})
	want := [][]string{{"a", "b"}, {"c"}}
	if got := f.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("Calls = %v", got)
	}
}

func TestFakeDescribeSetInfoAndFail(t *testing.T) {
	f := NewFake(8)
	info, err := f.Describe(context.Background())
	if err != nil || info.Dimensions != 8 || info.Digest != "fake" {
		t.Fatalf("default info = %+v, %v", info, err)
	}
	f.SetInfo(core.Info{Provider: "p", Model: "m", Digest: "d", Dimensions: 3})
	if info, _ = f.Describe(context.Background()); info.Model != "m" || info.Dimensions != 3 {
		t.Errorf("info = %+v", info)
	}
	boom := errors.New("boom")
	f.FailDescribe(boom)
	if _, err := f.Describe(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := f.Embed(context.Background(), []string{"x"}); err != nil {
		t.Errorf("Embed failed: %v", err)
	}
	f.RecoverDescribe()
	if _, err := f.Describe(context.Background()); err != nil {
		t.Errorf("err = %v", err)
	}
	if n := f.DescribeCalls(); n != 4 {
		t.Errorf("DescribeCalls = %d", n)
	}
}
