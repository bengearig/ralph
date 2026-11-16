package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentsMigrationApplies(t *testing.T) {
	d, err := openWith(t, filepath.Join(t.TempDir(), "docs.db"), Migrations[:6], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 6 {
		t.Errorf("version = %d, want 6", v)
	}
	if !hasTable(t, d, "documents") {
		t.Error("documents table missing")
	}
	if Migrations[5].Name != "create documents" {
		t.Errorf("migration 6 = %q", Migrations[5].Name)
	}
}

func TestVersionsMigrationApplies(t *testing.T) {
	last := Migrations[len(Migrations)-1]
	if last.Version != 7 || last.Name != "create document versions" {
		t.Fatalf("last migration = %d %q", last.Version, last.Name)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if v := version(t, fresh); v != 7 {
		t.Errorf("version = %d, want 7", v)
	}
	if !hasTable(t, fresh, "document_versions") {
		t.Error("document_versions table missing")
	}

	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:6], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, old); v != 6 {
		t.Fatalf("old version = %d, want 6", v)
	}
	_ = old.Close()
	migrated, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if v := version(t, migrated); v != 7 {
		t.Errorf("migrated version = %d, want 7", v)
	}
	a, b := schemaOf(t, fresh), schemaOf(t, migrated)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("schemas differ:\nfresh:\n%s\nmigrated:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

func seededDocuments(t *testing.T) *DB {
	t.Helper()
	d, err := openWith(t, filepath.Join(t.TempDir(), "doc.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	const cols = "(id, level, number, parent_id, position, title, state, created_at, updated_at)"
	for _, q := range []string{
		"INSERT INTO units " + cols + " VALUES ('B-1', 'brief', 1, NULL, 0, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('S-1', 'slice', 1, 'B-1', 1, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('T-1', 'task', 1, 'S-1', 1, 't', 'TODO', 'x', 'x')",
	} {
		if _, err := d.SQL().Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	return d
}

const docCols = "(id, number, kind, brief_id, title, body, status, superseded_by, created_at, updated_at)"

func insertDoc(d *DB, id string, number int, kind, brief, status, supersededBy any) error {
	_, err := d.SQL().Exec("INSERT INTO documents "+docCols+" VALUES (?, ?, ?, ?, 't', 'b', ?, ?, 'x', 'x')",
		id, number, kind, brief, status, supersededBy)
	return err
}

func TestDocumentsSchemaRejectsBadKindAndStatus(t *testing.T) {
	d := seededDocuments(t)
	if err := insertDoc(d, "D-1", 1, "notes", nil, "ACTIVE", nil); err == nil {
		t.Error("bad kind accepted")
	}
	if err := insertDoc(d, "D-2", 2, "design", nil, "DELETED", nil); err == nil {
		t.Error("bad status accepted")
	}
	if err := insertDoc(d, "D-3", 3, "design", nil, "ACTIVE", nil); err != nil {
		t.Errorf("valid row: %v", err)
	}
}

func TestDocumentsSchemaSupersededNeedsPointer(t *testing.T) {
	d := seededDocuments(t)
	if err := insertDoc(d, "D-1", 1, "design", nil, "ACTIVE", nil); err != nil {
		t.Fatal(err)
	}
	if err := insertDoc(d, "D-2", 2, "design", nil, "SUPERSEDED", nil); err == nil {
		t.Error("SUPERSEDED without superseded_by accepted")
	}
	if err := insertDoc(d, "D-3", 3, "design", nil, "ACTIVE", "D-1"); err == nil {
		t.Error("ACTIVE with superseded_by accepted")
	}
	if err := insertDoc(d, "D-4", 4, "design", nil, "SUPERSEDED", "D-1"); err != nil {
		t.Errorf("valid superseded row: %v", err)
	}
}

func TestDocumentsSchemaRejectsSelfSupersede(t *testing.T) {
	d := seededDocuments(t)
	if err := insertDoc(d, "D-1", 1, "design", nil, "SUPERSEDED", "D-1"); err == nil {
		t.Error("self supersede accepted")
	}
}

func TestDocumentsSchemaBriefTrigger(t *testing.T) {
	d := seededDocuments(t)
	for i, brief := range []string{"S-1", "T-1", "B-9"} {
		err := insertDoc(d, "D-"+string(rune('1'+i)), i+1, "design", brief, "ACTIVE", nil)
		if i < 2 && (err == nil || !strings.Contains(err.Error(), "a document is tied to a brief or to nothing")) {
			t.Errorf("%s: err = %v", brief, err)
		}
		if i == 2 && err == nil {
			t.Errorf("%s: missing unit accepted", brief)
		}
	}
	if err := insertDoc(d, "D-4", 4, "design", "B-1", "ACTIVE", nil); err != nil {
		t.Errorf("brief: %v", err)
	}
	if err := insertDoc(d, "D-5", 5, "design", nil, "ACTIVE", nil); err != nil {
		t.Errorf("NULL: %v", err)
	}
}

func TestDocumentsSchemaIdentityFixed(t *testing.T) {
	d := seededDocuments(t)
	if err := insertDoc(d, "D-1", 1, "design", "B-1", "ACTIVE", nil); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{"id = 'D-9'", "number = 9", "brief_id = NULL", "created_at = 'y'"} {
		_, err := d.SQL().Exec("UPDATE documents SET " + set + " WHERE id = 'D-1'")
		if err == nil || !strings.Contains(err.Error(), "id, number, brief and creation time of a document never change") {
			t.Errorf("%s: err = %v", set, err)
		}
	}
	if _, err := d.SQL().Exec("UPDATE documents SET title = 'new', updated_at = 'y' WHERE id = 'D-1'"); err != nil {
		t.Errorf("mutable fields: %v", err)
	}
}
