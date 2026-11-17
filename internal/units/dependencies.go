package units

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Dependency is a slice that another slice waits for. It is Satisfied once its
// derived status is MERGED, or DONE with no pull request recorded.
type Dependency struct {
	ID        string
	Title     string
	Status    Status
	Satisfied bool
}

func sliceIn(ctx context.Context, tx *sql.Tx, id, role string) error {
	u, err := getTx(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("%s: %w", role, err)
	}
	if u.Level != LevelSlice {
		return fmt.Errorf("%w: the %s must be a slice, and %s is a %s", ErrInvalid, role, id, u.Level)
	}
	return nil
}

// AddDependency makes a slice wait for another. Adding one that exists changes
// nothing; one that would close a cycle is refused.
func (s *Store) AddDependency(ctx context.Context, sliceID, dependsOnID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sliceIn(ctx, tx, sliceID, "waiting slice"); err != nil {
			return err
		}
		if err := sliceIn(ctx, tx, dependsOnID, "slice waited for"); err != nil {
			return err
		}
		if sliceID == dependsOnID {
			return fmt.Errorf("%w: %s cannot depend on itself", ErrInvalid, sliceID)
		}
		path, err := dependencyPath(ctx, tx, dependsOnID, sliceID)
		if err != nil {
			return err
		}
		if path != nil {
			cycle := append([]string{sliceID}, path...)
			return fmt.Errorf("%w: %s waiting for %s would make a cycle: %s", ErrInvalid, sliceID, dependsOnID, strings.Join(cycle, " -> "))
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO dependencies (slice_id, depends_on_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
			sliceID, dependsOnID, s.stamp()); err != nil {
			return fmt.Errorf("units: add dependency of %s on %s: %w", sliceID, dependsOnID, err)
		}
		return nil
	})
}

// dependencyPath returns the shortest chain of waits from one slice to
// another, both ends included, or nil if from does not wait on to.
func dependencyPath(ctx context.Context, tx *sql.Tx, from, to string) ([]string, error) {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		next, err := dependsOnIDs(ctx, tx, cur)
		if err != nil {
			return nil, err
		}
		for _, n := range next {
			if _, seen := prev[n]; seen {
				continue
			}
			prev[n] = cur
			if n == to {
				var path []string
				for at := n; at != ""; at = prev[at] {
					path = append([]string{at}, path...)
				}
				return path, nil
			}
			queue = append(queue, n)
		}
	}
	return nil, nil
}

func dependsOnIDs(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT d.depends_on_id FROM dependencies d JOIN units u ON u.id = d.depends_on_id WHERE d.slice_id = ? ORDER BY u.number", id)
	if err != nil {
		return nil, fmt.Errorf("units: dependencies of %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("units: dependencies of %s: %w", id, err)
		}
		out = append(out, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("units: dependencies of %s: %w", id, err)
	}
	return out, nil
}

// RemoveDependency removes a dependency; removing one that is not there is not
// an error.
func (s *Store) RemoveDependency(ctx context.Context, sliceID, dependsOnID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sliceIn(ctx, tx, sliceID, "waiting slice"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM dependencies WHERE slice_id = ? AND depends_on_id = ?", sliceID, dependsOnID); err != nil {
			return fmt.Errorf("units: remove dependency of %s on %s: %w", sliceID, dependsOnID, err)
		}
		return nil
	})
}

// Dependencies returns the slices a slice waits for, ordered by ID number,
// each with its derived status.
func (s *Store) Dependencies(ctx context.Context, sliceID string) ([]Dependency, error) {
	return s.readDependencies(ctx, sliceID, false)
}

// Waiting returns the dependencies of a slice that are not satisfied.
func (s *Store) Waiting(ctx context.Context, sliceID string) ([]Dependency, error) {
	return s.readDependencies(ctx, sliceID, true)
}

func (s *Store) readDependencies(ctx context.Context, sliceID string, onlyWaiting bool) ([]Dependency, error) {
	tx, err := s.db.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("units: dependencies of %s: %w", sliceID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := sliceIn(ctx, tx, sliceID, "slice"); err != nil {
		return nil, err
	}
	deps, err := dependenciesIn(ctx, tx, sliceID)
	if err != nil {
		return nil, err
	}
	if !onlyWaiting {
		return deps, nil
	}
	return unsatisfied(deps), nil
}

func unsatisfied(deps []Dependency) []Dependency {
	out := []Dependency{}
	for _, d := range deps {
		if !d.Satisfied {
			out = append(out, d)
		}
	}
	return out
}

func waitingIn(ctx context.Context, tx *sql.Tx, sliceID string) ([]Dependency, error) {
	deps, err := dependenciesIn(ctx, tx, sliceID)
	if err != nil {
		return nil, err
	}
	return unsatisfied(deps), nil
}

func dependenciesIn(ctx context.Context, tx *sql.Tx, sliceID string) ([]Dependency, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT u.id, u.title FROM dependencies d JOIN units u ON u.id = d.depends_on_id WHERE d.slice_id = ? ORDER BY u.number", sliceID)
	if err != nil {
		return nil, fmt.Errorf("units: dependencies of %s: %w", sliceID, err)
	}
	out := []Dependency{}
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.ID, &d.Title); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("units: dependencies of %s: %w", sliceID, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("units: dependencies of %s: %w", sliceID, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("units: dependencies of %s: %w", sliceID, err)
	}
	memo := map[string]Status{}
	for i := range out {
		st, err := statusIn(ctx, tx, out[i].ID, memo)
		if err != nil {
			return nil, err
		}
		out[i].Status = st
		switch st {
		case StatusMerged:
			out[i].Satisfied = true
		case StatusDone:
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pull_requests WHERE slice_id = ?", out[i].ID).Scan(&n); err != nil {
				return nil, fmt.Errorf("units: pull requests of %s: %w", out[i].ID, err)
			}
			out[i].Satisfied = n == 0
		}
	}
	return out, nil
}

// Dependents returns the slices that wait for a slice, directly or through
// others, each once, ordered by ID number.
func (s *Store) Dependents(ctx context.Context, sliceID string) ([]string, error) {
	tx, err := s.db.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("units: dependents of %s: %w", sliceID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := sliceIn(ctx, tx, sliceID, "slice"); err != nil {
		return nil, err
	}
	seen := map[string]bool{sliceID: true}
	queue := []string{sliceID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := tx.QueryContext(ctx, "SELECT slice_id FROM dependencies WHERE depends_on_id = ?", cur)
		if err != nil {
			return nil, fmt.Errorf("units: dependents of %s: %w", sliceID, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("units: dependents of %s: %w", sliceID, err)
			}
			if !seen[id] {
				seen[id] = true
				queue = append(queue, id)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("units: dependents of %s: %w", sliceID, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("units: dependents of %s: %w", sliceID, err)
		}
	}
	delete(seen, sliceID)
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return idNumber(out[i]) < idNumber(out[j]) })
	return out, nil
}

func idNumber(id string) int {
	n, _ := strconv.Atoi(id[strings.IndexByte(id, '-')+1:])
	return n
}
