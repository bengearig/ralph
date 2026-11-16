package db

const createUnitsSQL = `
CREATE TABLE units (
	id TEXT PRIMARY KEY,
	level TEXT NOT NULL,
	number INTEGER NOT NULL,
	parent_id TEXT REFERENCES units(id),
	position INTEGER NOT NULL DEFAULT 0,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	acceptance_criteria TEXT NOT NULL DEFAULT '',
	state TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (level, number),
	CHECK (level IN ('brief', 'slice', 'task')),
	CHECK (level <> 'brief' OR parent_id IS NULL),
	CHECK (level <> 'slice' OR parent_id IS NOT NULL),
	CHECK ((level = 'task') = (state IS NOT NULL)),
	CHECK (state IS NULL OR state IN ('PROPOSED', 'TODO', 'IN_PROGRESS', 'BLOCKED', 'CANCELLED', 'DONE')),
	CHECK (level = 'task' OR acceptance_criteria = ''),
	CHECK (parent_id IS NOT NULL OR position = 0)
);

CREATE INDEX units_parent_position ON units (parent_id, position);

CREATE TABLE counters (
	level TEXT PRIMARY KEY,
	last INTEGER NOT NULL
);

CREATE TRIGGER units_parent_level BEFORE INSERT ON units
WHEN NEW.parent_id IS NOT NULL
BEGIN
	SELECT RAISE(ABORT, 'a slice must have a brief as its parent')
	WHERE NEW.level = 'slice'
		AND (SELECT level FROM units WHERE id = NEW.parent_id) IS NOT 'brief';
	SELECT RAISE(ABORT, 'a task must have a slice as its parent or none')
	WHERE NEW.level = 'task'
		AND (SELECT level FROM units WHERE id = NEW.parent_id) IS NOT 'slice';
END;

CREATE TRIGGER units_identity_fixed BEFORE UPDATE ON units
WHEN NEW.id IS NOT OLD.id
	OR NEW.level IS NOT OLD.level
	OR NEW.number IS NOT OLD.number
	OR NEW.parent_id IS NOT OLD.parent_id
BEGIN
	SELECT RAISE(ABORT, 'id, level, number and parent_id of a unit never change');
END;
`

const createLinksSQL = `
CREATE TABLE links (
	unit_id TEXT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
	kind TEXT NOT NULL CHECK (kind IN ('document', 'external')),
	target TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (unit_id, kind, target)
);
`

const createNotesSQL = `
CREATE TABLE notes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	unit_id TEXT NOT NULL REFERENCES units(id),
	kind TEXT NOT NULL,
	body TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX notes_unit ON notes (unit_id, id);
`

const recordActorsSQL = `
ALTER TABLE units ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE units ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';
ALTER TABLE notes ADD COLUMN author TEXT NOT NULL DEFAULT '';
`

const createDependenciesSQL = `
CREATE TABLE dependencies (
	slice_id TEXT NOT NULL REFERENCES units(id),
	depends_on_id TEXT NOT NULL REFERENCES units(id),
	created_at TEXT NOT NULL,
	PRIMARY KEY (slice_id, depends_on_id),
	CHECK (slice_id <> depends_on_id)
);

CREATE INDEX dependencies_depends_on ON dependencies (depends_on_id);

CREATE TRIGGER dependencies_join_slices BEFORE INSERT ON dependencies
BEGIN
	SELECT RAISE(ABORT, 'a dependency joins two slices')
	WHERE (SELECT level FROM units WHERE id = NEW.slice_id) IS NOT 'slice'
		OR (SELECT level FROM units WHERE id = NEW.depends_on_id) IS NOT 'slice';
END;
`

const createDocumentsSQL = `
CREATE TABLE documents (
	id TEXT PRIMARY KEY,
	number INTEGER NOT NULL UNIQUE,
	kind TEXT NOT NULL CHECK (kind IN ('architecture', 'design', 'decision', 'reference')),
	brief_id TEXT REFERENCES units(id),
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'ARCHIVED', 'SUPERSEDED')),
	superseded_by TEXT REFERENCES documents(id),
	version INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	created_by TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT '',
	CHECK ((status = 'SUPERSEDED') = (superseded_by IS NOT NULL)),
	CHECK (superseded_by IS NULL OR superseded_by <> id)
);

CREATE INDEX documents_brief ON documents (brief_id);

CREATE TRIGGER documents_brief_level BEFORE INSERT ON documents
WHEN NEW.brief_id IS NOT NULL
BEGIN
	SELECT RAISE(ABORT, 'a document is tied to a brief or to nothing')
	WHERE (SELECT level FROM units WHERE id = NEW.brief_id) IS NOT 'brief';
END;

CREATE TRIGGER documents_identity_fixed BEFORE UPDATE ON documents
WHEN NEW.id IS NOT OLD.id
	OR NEW.number IS NOT OLD.number
	OR NEW.brief_id IS NOT OLD.brief_id
	OR NEW.created_at IS NOT OLD.created_at
BEGIN
	SELECT RAISE(ABORT, 'id, number, brief and creation time of a document never change');
END;
`

const createDocumentVersionsSQL = `
CREATE TABLE document_versions (
	document_id TEXT NOT NULL REFERENCES documents(id),
	version INTEGER NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	created_at TEXT NOT NULL,
	created_by TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (document_id, version)
);
`

const createSearchIndexSQL = `
CREATE VIRTUAL TABLE search_fts USING fts5(
	title, body, kind UNINDEXED, ref UNINDEXED, unit UNINDEXED,
	tokenize = 'porter unicode61 remove_diacritics 2'
);
`
