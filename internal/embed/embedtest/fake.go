// Package embedtest provides a deterministic embed.Provider for tests.
package embedtest

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bengearig/ralph/internal/embed/core"
)

const defaultDims = 64

// Vector returns the normalized vector of text: each lowercase word is hashed into a dimension with a sign
// and the signed counts are summed, so texts that share words have a higher dot product. Text with no
// letters or digits gives the vector with a single 1 in the first dimension.
func Vector(text string, dims int) []float32 {
	if dims <= 0 {
		dims = defaultDims
	}
	v := make([]float32, dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		h := fnv.New64a()
		_, _ = h.Write([]byte(w))
		sum := h.Sum64()
		sign := float32(1)
		if sum>>63 == 1 {
			sign = -1
		}
		v[int(sum%uint64(dims))] += sign
	}
	n, err := core.Normalize(v)
	if err != nil {
		v = make([]float32, dims)
		v[0] = 1
		return v
	}
	return n
}

// Fake is a deterministic provider that records its calls and can be made to fail or wait.
type Fake struct {
	dims int

	mu    sync.Mutex
	calls [][]string
	err   error
	delay time.Duration

	info         *core.Info
	describeErr  error
	describeCall int
}

// NewFake returns a Fake with vectors of dims values; zero or less means the default.
func NewFake(dims int) *Fake {
	if dims <= 0 {
		dims = defaultDims
	}
	return &Fake{dims: dims}
}

// Embed records the texts and returns their vectors.
func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	err, delay := f.err, f.delay
	f.mu.Unlock()
	if err := wait(ctx, delay); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = Vector(t, f.dims)
	}
	return out, nil
}

// Describe reports the fake model.
func (f *Fake) Describe(ctx context.Context) (core.Info, error) {
	f.mu.Lock()
	f.describeCall++
	err, delay := f.err, f.delay
	if f.describeErr != nil {
		err = f.describeErr
	}
	info := core.Info{Provider: "fake", Model: "fake", Digest: "fake", Dimensions: f.dims}
	if f.info != nil {
		info = *f.info
	}
	f.mu.Unlock()
	if err := wait(ctx, delay); err != nil {
		return core.Info{}, err
	}
	if err != nil {
		return core.Info{}, err
	}
	return info, nil
}

// SetInfo sets what Describe returns.
func (f *Fake) SetInfo(info core.Info) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info = &info
}

// FailDescribe makes Describe, and only Describe, fail with err until RecoverDescribe.
func (f *Fake) FailDescribe(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.describeErr = err
}

// RecoverDescribe ends the failure set by FailDescribe.
func (f *Fake) RecoverDescribe() { f.FailDescribe(nil) }

// DescribeCalls counts the Describe calls.
func (f *Fake) DescribeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.describeCall
}

// Calls returns the texts of each Embed call.
func (f *Fake) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// FailWith makes every call fail with err until Recover.
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Recover ends the failure set by FailWith.
func (f *Fake) Recover() { f.FailWith(nil) }

// SetDelay makes every call sleep for d before it answers, returning early if the context ends.
func (f *Fake) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
