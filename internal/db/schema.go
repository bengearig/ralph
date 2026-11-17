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

const indexDocumentLinksSQL = `
CREATE INDEX links_target ON links (kind, target);
`

const indexRecordsForSearchSQL = `
INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
SELECT number * 5 + CASE level WHEN 'brief' THEN 0 WHEN 'slice' THEN 1 ELSE 2 END,
	title,
	body || CASE WHEN acceptance_criteria <> '' THEN char(10) || acceptance_criteria ELSE '' END,
	level, id, ''
FROM units;

INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
SELECT number * 5 + 3, title, body, 'document', id, ''
FROM documents;

INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
SELECT id * 5 + 4, '', body, 'note', CAST(id AS TEXT), unit_id
FROM notes;

CREATE TRIGGER units_search_insert AFTER INSERT ON units
BEGIN
	INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
	VALUES (
		NEW.number * 5 + CASE NEW.level WHEN 'brief' THEN 0 WHEN 'slice' THEN 1 ELSE 2 END,
		NEW.title,
		NEW.body || CASE WHEN NEW.acceptance_criteria <> '' THEN char(10) || NEW.acceptance_criteria ELSE '' END,
		NEW.level, NEW.id, ''
	);
END;

CREATE TRIGGER units_search_update AFTER UPDATE OF title, body, acceptance_criteria ON units
BEGIN
	DELETE FROM search_fts
	WHERE rowid = OLD.number * 5 + CASE OLD.level WHEN 'brief' THEN 0 WHEN 'slice' THEN 1 ELSE 2 END;
	INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
	VALUES (
		NEW.number * 5 + CASE NEW.level WHEN 'brief' THEN 0 WHEN 'slice' THEN 1 ELSE 2 END,
		NEW.title,
		NEW.body || CASE WHEN NEW.acceptance_criteria <> '' THEN char(10) || NEW.acceptance_criteria ELSE '' END,
		NEW.level, NEW.id, ''
	);
END;

CREATE TRIGGER units_search_delete AFTER DELETE ON units
BEGIN
	DELETE FROM search_fts
	WHERE rowid = OLD.number * 5 + CASE OLD.level WHEN 'brief' THEN 0 WHEN 'slice' THEN 1 ELSE 2 END;
END;

CREATE TRIGGER documents_search_insert AFTER INSERT ON documents
BEGIN
	INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
	VALUES (NEW.number * 5 + 3, NEW.title, NEW.body, 'document', NEW.id, '');
END;

CREATE TRIGGER documents_search_update AFTER UPDATE OF title, body ON documents
BEGIN
	DELETE FROM search_fts WHERE rowid = OLD.number * 5 + 3;
	INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
	VALUES (NEW.number * 5 + 3, NEW.title, NEW.body, 'document', NEW.id, '');
END;

CREATE TRIGGER documents_search_delete AFTER DELETE ON documents
BEGIN
	DELETE FROM search_fts WHERE rowid = OLD.number * 5 + 3;
END;

CREATE TRIGGER notes_search_insert AFTER INSERT ON notes
BEGIN
	INSERT INTO search_fts (rowid, title, body, kind, ref, unit)
	VALUES (NEW.id * 5 + 4, '', NEW.body, 'note', CAST(NEW.id AS TEXT), NEW.unit_id);
END;

CREATE TRIGGER notes_search_delete AFTER DELETE ON notes
BEGIN
	DELETE FROM search_fts WHERE rowid = OLD.id * 5 + 4;
END;
`

const createPullRequestsSQL = `
CREATE TABLE pull_requests (
	slice_id   TEXT NOT NULL REFERENCES units(id),
	number     INTEGER NOT NULL,
	url        TEXT NOT NULL,
	head       TEXT NOT NULL,
	base       TEXT NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('open', 'closed', 'merged')),
	resolution TEXT NOT NULL DEFAULT '' CHECK (resolution IN ('', 'reopened', 'rearticulated', 'cancelled')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (slice_id, number)
);

CREATE INDEX pull_requests_by_slice ON pull_requests (slice_id, created_at);

CREATE TRIGGER pull_requests_slice_only BEFORE INSERT ON pull_requests
WHEN (SELECT level FROM units WHERE id = NEW.slice_id) IS NOT 'slice'
BEGIN
	SELECT RAISE(ABORT, 'a pull request belongs to a slice');
END;
`

const createTicketsSQL = `
CREATE TABLE tickets (
	unit_id        TEXT PRIMARY KEY REFERENCES units(id) ON DELETE CASCADE,
	system         TEXT NOT NULL,
	ticket_key     TEXT NOT NULL,
	ticket_id      TEXT NOT NULL DEFAULT '',
	pushed_title   TEXT NOT NULL DEFAULT '',
	pushed_body    TEXT NOT NULL DEFAULT '',
	pushed_status  TEXT NOT NULL DEFAULT '',
	pushed_parent  TEXT NOT NULL DEFAULT '',
	scheduled      INTEGER NOT NULL DEFAULT 0,
	link_confirmed INTEGER NOT NULL DEFAULT 0,
	drift          TEXT NOT NULL DEFAULT '',
	drift_seen_at  TEXT NOT NULL DEFAULT '',
	failed_step    TEXT NOT NULL DEFAULT '',
	failed_at      TEXT NOT NULL DEFAULT '',
	last_error     TEXT NOT NULL DEFAULT '',
	attempts       INTEGER NOT NULL DEFAULT 0,
	synced_at      TEXT NOT NULL DEFAULT '',
	UNIQUE (system, ticket_key)
);

CREATE TABLE ticket_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

const addMappedStatusSQL = `
ALTER TABLE tickets ADD COLUMN mapped_status TEXT NOT NULL DEFAULT '';
`
