// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
)

// TestOpenSetsThePragmasThatHoldOneWriterAndManyReadersTogether.
//
// Without WAL a chart drawn while a pass is committing is a SQLITE_BUSY in the
// browser, which is a bug a user meets and cannot explain. The other three are
// the same argument: a busy_timeout of zero turns a concurrent read into an
// error, and foreign_keys defaults to OFF in SQLite, so every ON DELETE
// CASCADE in the schema would be decoration.
func TestOpenSetsThePragmasThatHoldOneWriterAndManyReadersTogether(t *testing.T) {
	s := openTemp(t)

	want := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"foreign_keys": "1",
		"busy_timeout": "5000",
	}
	for pragma, expected := range want {
		var got string
		if err := s.DB().QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != expected {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, expected)
		}
	}
}

// TestOpenIsIdempotent. A second Open of the same file must find the migration
// already applied and not attempt it again.
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "avito.db")

	s1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	// The count must equal the number of embedded migrations and must not have
	// grown between the two Opens. Asserting a literal number here would fail
	// every time a migration is added, which is a test that has to be edited to
	// stay true rather than one that says something.
	want, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s2.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Errorf("schema_migrations has %d rows after two Opens, want %d — one "+
			"per embedded migration and no more", n, len(want))
	}
}

// openTemp is the fixture every test in this package uses.
func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "avito.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
