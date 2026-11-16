package documents

import (
	"errors"

	"github.com/bengearig/ralph/internal/db"
)

var (
	ErrNotFound = db.ErrNotFound
	ErrInvalid  = errors.New("invalid input")
)
