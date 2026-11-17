package embed

import (
	"fmt"
	"strings"
)

// Fingerprint is the configuration that produced a set of embeddings; vectors made by different
// fingerprints cannot be compared.
type Fingerprint struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Digest         string `json:"digest,omitempty"`
	Dimensions     int    `json:"dimensions,omitempty"`
	DocumentPrefix string `json:"document_prefix"`
	QueryPrefix    string `json:"query_prefix"`
	ChunkChars     int    `json:"chunk_chars"`
}

// NormalizeModel removes a trailing :latest tag and nothing else.
func NormalizeModel(name string) string {
	return strings.TrimSuffix(name, ":latest")
}

// NewFingerprint describes the configuration of s and the model info reports.
func NewFingerprint(s Settings, info Info) Fingerprint {
	doc, qry := Prefixes(s)
	return Fingerprint{
		Provider:       s.Provider,
		Model:          NormalizeModel(s.Model),
		Digest:         info.Digest,
		Dimensions:     info.Dimensions,
		DocumentPrefix: doc,
		QueryPrefix:    qry,
		ChunkChars:     s.ChunkChars,
	}
}

// Differences lists what differs between the recorded and the current fingerprint; nil means equal.
func Differences(recorded, current Fingerprint) []string {
	var out []string
	if recorded.Provider != current.Provider {
		out = append(out, fmt.Sprintf("provider %s -> %s", recorded.Provider, current.Provider))
	}
	if recorded.Model != current.Model {
		out = append(out, fmt.Sprintf("model %s -> %s", recorded.Model, current.Model))
	}
	if recorded.Digest != "" && current.Digest != "" && recorded.Digest != current.Digest {
		out = append(out, fmt.Sprintf("digest %s -> %s", shortDigest(recorded.Digest), shortDigest(current.Digest)))
	}
	if recorded.Dimensions != 0 && current.Dimensions != 0 && recorded.Dimensions != current.Dimensions {
		out = append(out, fmt.Sprintf("dimensions %d -> %d", recorded.Dimensions, current.Dimensions))
	}
	if recorded.DocumentPrefix != current.DocumentPrefix {
		out = append(out, fmt.Sprintf("document prefix %q -> %q", recorded.DocumentPrefix, current.DocumentPrefix))
	}
	if recorded.QueryPrefix != current.QueryPrefix {
		out = append(out, fmt.Sprintf("query prefix %q -> %q", recorded.QueryPrefix, current.QueryPrefix))
	}
	if recorded.ChunkChars != current.ChunkChars {
		out = append(out, fmt.Sprintf("chunk size %d -> %d", recorded.ChunkChars, current.ChunkChars))
	}
	return out
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
