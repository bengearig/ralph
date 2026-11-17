// Package embed defines the embedding provider interface used by semantic search.
package embed

import (
	"context"

	"github.com/bengearig/ralph/internal/embed/core"
)

// Provider turns texts into vectors of length 1.
type Provider interface {
	// Embed returns one vector per text, in order, all the same length.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Describe reports the provider's model.
	Describe(ctx context.Context) (Info, error)
}

// Info describes a provider's model.
type Info = core.Info

var (
	ErrUnavailable   = core.ErrUnavailable
	ErrModelNotFound = core.ErrModelNotFound
	ErrInvalid       = core.ErrInvalid
)

// Normalize returns v scaled to length 1; an all-zero, NaN or infinite vector is an error wrapping ErrInvalid.
func Normalize(v []float32) ([]float32, error) { return core.Normalize(v) }
