package cli

import (
	"strconv"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
)

// newDocumentsService builds the document service with the project's
// documents.versions.keep retention.
func newDocumentsService(a *app, d *db.DB) documents.Service {
	keep := 0
	if v, ok := a.cfg.Get("documents.versions.keep"); ok {
		if n, err := strconv.Atoi(v.Value); err == nil {
			keep = n
		}
	}
	return documents.NewService(d, documents.WithKeepVersions(keep))
}
