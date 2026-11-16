package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestFingerprintShape(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(Fingerprint("a", "b")) {
		t.Fatal("not 64 lowercase hex characters")
	}
}

func TestFingerprintIsStable(t *testing.T) {
	a, b := Fingerprint("x", "y"), Fingerprint("x", "y")
	if a != b {
		t.Fatal("unstable")
	}
}

func TestFingerprintFieldBoundaries(t *testing.T) {
	if Fingerprint("ab", "c") == Fingerprint("a", "bc") {
		t.Error(`("ab","c") equals ("a","bc")`)
	}
	if Fingerprint("a") == Fingerprint("a", "") {
		t.Error(`("a") equals ("a","")`)
	}
}

func TestFingerprintNoFields(t *testing.T) {
	if got := Fingerprint(); len(got) != 64 {
		t.Fatalf("len = %d", len(got))
	}
}

func recordDB(t *testing.T) *DB {
	t.Helper()
	d := openT(t, filepath.Join(t.TempDir(), "store.db"))
	if _, err := d.SQL().Exec("CREATE TABLE rec (id INTEGER PRIMARY KEY, v INTEGER); INSERT INTO rec VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}
	return d
}

func recCurrent(tx *sql.Tx) (string, error) {
	var v int
	err := tx.QueryRow("SELECT v FROM rec WHERE id = 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return currentFor(v)
}

func recSet(v int) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE rec SET v = ? WHERE id = 1", v)
		return err
	}
}

func recValue(t *testing.T, d *DB) int {
	t.Helper()
	var v int
	if err := d.SQL().QueryRow("SELECT v FROM rec WHERE id = 1").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func readFP(t *testing.T, d *DB) string {
	t.Helper()
	tx, err := d.SQL().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	fp, err := recCurrent(tx)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestCheckedWriteSucceeds(t *testing.T) {
	d := recordDB(t)
	if err := d.CheckedWrite(context.Background(), "rec", readFP(t, d), recCurrent, recSet(7)); err != nil {
		t.Fatal(err)
	}
	if v := recValue(t, d); v != 7 {
		t.Fatalf("v = %d", v)
	}
}

func TestCheckedWriteStale(t *testing.T) {
	d := recordDB(t)
	ctx := context.Background()
	a := readFP(t, d)
	if err := d.CheckedWrite(ctx, "rec", readFP(t, d), recCurrent, recSet(2)); err != nil {
		t.Fatal(err)
	}
	err := d.CheckedWrite(ctx, "rec", a, recCurrent, recSet(9))
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v", err)
	}
	var se *StaleError
	if !errors.As(err, &se) || se.Record != "rec" || se.Expected != a {
		t.Fatalf("stale error = %#v", err)
	}
	if v := recValue(t, d); v != 2 {
		t.Fatalf("v = %d, want B's value 2", v)
	}
}

func TestStaleErrorMessage(t *testing.T) {
	e := &StaleError{Record: "T-1", Expected: strings.Repeat("a", 64), Found: strings.Repeat("b", 64)}
	msg := e.Error()
	for _, want := range []string{`"T-1"`, strings.Repeat("a", 12), strings.Repeat("b", 12), "re-read it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, strings.Repeat("a", 13)) {
		t.Errorf("%q not truncated", msg)
	}
}

func TestCheckedWriteNoLostUpdates(t *testing.T) {
	d := recordDB(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				for {
					var v int
					if err := d.SQL().QueryRow("SELECT v FROM rec WHERE id = 1").Scan(&v); err != nil {
						t.Error(err)
						return
					}
					fp, _ := currentFor(v)
					err := d.CheckedWrite(ctx, "rec", fp, recCurrent, recSet(v+1))
					if err == nil {
						break
					}
					if !errors.Is(err, ErrStale) {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if v := recValue(t, d); v != 160 {
		t.Fatalf("v = %d, want 160", v)
	}
}

func currentFor(v int) (string, error) {
	return Fingerprint("rec", strconv.Itoa(v)), nil
}

func TestCheckedWriteNotFound(t *testing.T) {
	d := recordDB(t)
	if _, err := d.SQL().Exec("DELETE FROM rec"); err != nil {
		t.Fatal(err)
	}
	called := false
	err := d.CheckedWrite(context.Background(), "rec", "abc", recCurrent, func(*sql.Tx) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("write called")
	}
}

func TestCheckedWriteRollsBackOnWriteError(t *testing.T) {
	d := recordDB(t)
	boom := errors.New("boom")
	err := d.CheckedWrite(context.Background(), "rec", readFP(t, d), recCurrent, func(tx *sql.Tx) error {
		if err := recSet(5)(tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if v := recValue(t, d); v != 0 {
		t.Fatalf("v = %d, want 0", v)
	}
}

func TestCheckedWriteRequiresExpected(t *testing.T) {
	d := recordDB(t)
	called := false
	err := d.CheckedWrite(context.Background(), "rec", "", func(*sql.Tx) (string, error) {
		called = true
		return "", nil
	}, func(*sql.Tx) error { called = true; return nil })
	if err == nil || errors.Is(err, ErrStale) || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}
