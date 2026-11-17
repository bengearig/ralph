package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/vecindex"
)

const (
	metaConfig  = "config"
	metaBuiltAt = "built_at"
)

// ReadConfig returns the configuration recorded with the index; false means none is recorded.
func ReadConfig(ctx context.Context, idx *vecindex.Store) (embed.Fingerprint, bool, error) {
	v, ok, err := idx.Meta(ctx, metaConfig)
	if err != nil || !ok {
		return embed.Fingerprint{}, false, err
	}
	var fp embed.Fingerprint
	if err := json.Unmarshal([]byte(v), &fp); err != nil {
		return embed.Fingerprint{}, false, fmt.Errorf("the configuration recorded with the semantic index is unreadable: %w", err)
	}
	return fp, true, nil
}

// RecordConfig records the configuration that built the index and when.
func RecordConfig(ctx context.Context, idx *vecindex.Store, fp embed.Fingerprint, now time.Time) error {
	b, err := json.Marshal(fp)
	if err != nil {
		return fmt.Errorf("record the semantic index configuration: %w", err)
	}
	if err := idx.SetMeta(ctx, metaConfig, string(b)); err != nil {
		return err
	}
	return idx.SetMeta(ctx, metaBuiltAt, now.UTC().Format(time.RFC3339))
}
