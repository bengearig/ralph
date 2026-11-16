package units

import (
	"errors"

	"github.com/bengearig/ralph/internal/db"
)

var (
	ErrNotFound      = db.ErrNotFound
	ErrInvalid       = errors.New("invalid input")
	ErrInvalidParent = errors.New("invalid parent")
)
