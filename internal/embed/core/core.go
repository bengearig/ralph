// Package core holds the types and errors shared by internal/embed and the
// providers it constructs; use them through internal/embed.
package core

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrUnavailable means the service cannot be reached, timed out or failed.
	ErrUnavailable = errors.New("embedding service unavailable")
	// ErrModelNotFound means the service does not have the configured model.
	ErrModelNotFound = errors.New("embedding model not found")
	// ErrInvalid means the service refused the input or returned unusable vectors.
	ErrInvalid = errors.New("invalid embedding input or response")
)

// Info describes a provider's model.
type Info struct {
	Provider      string
	Model         string
	Digest        string
	Dimensions    int
	ContextTokens int
}

// Normalize returns v scaled to length 1.
func Normalize(v []float32) ([]float32, error) {
	var sum float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: vector contains a NaN or infinite value", ErrInvalid)
		}
		sum += f * f
	}
	if sum == 0 {
		return nil, fmt.Errorf("%w: vector is all zeros", ErrInvalid)
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out, nil
}
