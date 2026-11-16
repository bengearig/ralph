package db

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Fingerprint returns the hex SHA-256 over fields, each preceded by its
// 8-byte big-endian length so that field boundaries matter.
func Fingerprint(fields ...string) string {
	h := sha256.New()
	var n [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}
