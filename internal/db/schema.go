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
