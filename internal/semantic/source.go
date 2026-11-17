// Package semantic keeps the vector index in step with the project's records.
package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/search"
)

// Record is one thing that is indexed, with the text that keyword search indexes for it.
type Record struct {
	Kind  search.Kind
	Ref   string
	Unit  string
	Title string
	Text  string
	Hash  string
}

const unitTextSQL = `body || CASE WHEN acceptance_criteria <> '' THEN char(10) || acceptance_criteria ELSE '' END`

const unitsSQL = `SELECT id, title, ` + unitTextSQL + ` FROM units WHERE level = ? ORDER BY number`

const documentsSQL = `SELECT id, title, body FROM documents ORDER BY number`

const notesSQL = `SELECT CAST(id AS TEXT), unit_id, body FROM notes ORDER BY id`

// Records reads every brief, slice, task, document and note, in that order, each by number (notes by id).
func Records(ctx context.Context, d *db.DB) ([]Record, error) {
	var out []Record
	for _, kind := range []search.Kind{search.KindBrief, search.KindSlice, search.KindTask} {
		err := collect(ctx, d, unitsSQL, []any{string(kind)}, func(s scanner) error {
			r := Record{Kind: kind}
			if err := s.Scan(&r.Ref, &r.Title, &r.Text); err != nil {
				return err
			}
			out = append(out, finish(r))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	err := collect(ctx, d, documentsSQL, nil, func(s scanner) error {
		r := Record{Kind: search.KindDocument}
		if err := s.Scan(&r.Ref, &r.Title, &r.Text); err != nil {
			return err
		}
		out = append(out, finish(r))
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = collect(ctx, d, notesSQL, nil, func(s scanner) error {
		r := Record{Kind: search.KindNote}
		if err := s.Scan(&r.Ref, &r.Unit, &r.Text); err != nil {
			return err
		}
		out = append(out, finish(r))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type scanner interface{ Scan(dest ...any) error }

func collect(ctx context.Context, d *db.DB, query string, args []any, each func(scanner) error) error {
	rows, err := d.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("semantic: read records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := each(rows); err != nil {
			return fmt.Errorf("semantic: read records: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("semantic: read records: %w", err)
	}
	return nil
}

func finish(r Record) Record {
	sum := sha256.New()
	sum.Write([]byte(string(r.Kind)))
	sum.Write([]byte{0})
	sum.Write([]byte(r.Title))
	sum.Write([]byte{0})
	sum.Write([]byte(r.Text))
	r.Hash = hex.EncodeToString(sum.Sum(nil))
	return r
}
