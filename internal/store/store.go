// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the SQLite file: the schema, the pass transaction, the
// reads, retention, and the numbers that say what it all costs.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// ErrClosed is returned by a Store that has been closed.
var ErrClosed = errors.New("store: closed")

// Store is one database file.
//
// One connection writes and the panel's readers do not block it. The write
// mutex is here rather than left to SQLite because a pass is several
// statements that must not interleave with another pass's, and SQLITE_BUSY
// under WAL is a retry loop nobody wants in the hot path.
type Store struct {
	db   *sql.DB
	path string
	wmu  sync.Mutex

	now func() time.Time

	// retention is settable because the spec says both body bounds are
	// settings and neither may be unset. An unset struct means the defaults,
	// never "unbounded".
	retention atomic.Pointer[Retention]
}

// Retention is the thresholds this store thins by. A zero value has never been
// set and means the defaults; it never means no limit, because an archive with
// no limit is unbounded on a broken install and that is exactly when it grows
// fastest.
func (s *Store) Retention() Retention {
	if r := s.retention.Load(); r != nil {
		return *r
	}
	return DefaultRetention()
}

func (s *Store) SetRetention(r Retention) { s.retention.Store(&r) }

// pragmas are set on every connection in the pool, which is why they travel in
// the DSN rather than being executed once after Open: database/sql opens
// connections lazily and a PRAGMA run on one of them binds to that connection
// alone.
//
//	journal_mode=WAL   one writer, many readers. Without it a chart drawn while
//	                   a pass is committing is a SQLITE_BUSY in the browser.
//	synchronous=NORMAL the WAL-appropriate setting. The trade is named in the
//	                   spec: a power cut can lose the last transactions, which
//	                   for a monitor means one pass, and a pass is re-collected
//	                   on the next tick.
//	busy_timeout=5000  wait rather than fail.
//	foreign_keys=ON    SQLite defaults this OFF, which would make every
//	                   ON DELETE CASCADE in the schema decoration.
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)"

// Open opens or creates the database and brings it to the latest schema.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// A small pool. More connections would not make writes faster — SQLite
	// serialises them — and would turn a contended pass into a SQLITE_BUSY
	// instead of a wait.
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db, path: path, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DB is the handle, for readers and for tests. Writers go through the methods
// on Store, which take the write lock.
func (s *Store) DB() *sql.DB { return s.db }

// Path is the file this Store was opened on.
func (s *Store) Path() string { return s.path }

func (s *Store) Close() error { return s.db.Close() }
