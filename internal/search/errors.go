package search

import "errors"

// ErrInvalid reports a query that cannot be run: no words, too many words, an
// unknown kind or a limit out of range.
var ErrInvalid = errors.New("invalid input")
