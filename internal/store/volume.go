// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Volume is what the database costs right now.
//
// Lifted from WB_GO's internal/store/vacuum.go, whose own comment is the whole
// argument: nobody administers this product — it is one file a person started
// by double clicking it — and without a number they can look at, the first time
// they learn the history is growing is the day the disk fills.
type Volume struct {
	// FileBytes is what the database occupies on disk, the write-ahead log and
	// its shared-memory index included.
	FileBytes int64
	// Rows is the row count of every table in the schema, empty ones included.
	// A table missing from this map is a table this build does not know about;
	// a table with zero is a table with nothing in it, and the difference
	// matters when reading a growth report.
	Rows map[string]int64
}

// String is what `avitomon volume` prints.
func (v Volume) String() string {
	names := make([]string, 0, len(v.Rows))
	for n := range v.Rows {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "file  %.1f MB\n", float64(v.FileBytes)/(1<<20))
	for _, n := range names {
		fmt.Fprintf(&b, "%-20s %8d\n", n, v.Rows[n])
	}
	return b.String()
}

// Volume measures the database.
//
// The counts are exact COUNT(*) rather than the planner's estimates: the
// numbers are read by a human deciding whether to thin or to narrow a watch,
// and an estimate wrong by a factor of two makes that decision for them.
func (s *Store) Volume(ctx context.Context) (Volume, error) {
	v := Volume{Rows: map[string]int64{}}

	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return Volume{}, fmt.Errorf("store: volume: list tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return Volume{}, fmt.Errorf("store: volume: list tables: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Volume{}, fmt.Errorf("store: volume: list tables: %w", err)
	}
	rows.Close()

	for _, name := range names {
		// A table name cannot be a bound parameter, so it is quoted instead.
		// These names come from sqlite_master and are safe today; quoting costs
		// one line and stays correct the day someone counts a table whose name
		// came from somewhere else.
		q := `SELECT COUNT(*) FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		var n int64
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return Volume{}, fmt.Errorf("store: volume: count %s: %w", name, err)
		}
		v.Rows[name] = n
	}

	b, err := s.fileBytes(ctx)
	if err != nil {
		return Volume{}, err
	}
	v.FileBytes = b
	return v, nil
}

// fileBytes measures the database and its sidecars on disk.
//
// The write-ahead log holds every page written since the last checkpoint and
// can outgrow the database itself while a long-running reader keeps a
// checkpoint from completing. Reporting the main file alone would say the
// database is small on precisely the day it is not.
func (s *Store) fileBytes(ctx context.Context) (int64, error) {
	var file sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)
	if err != nil {
		return 0, fmt.Errorf("store: volume: locate the database file: %w", err)
	}
	if !file.Valid || file.String == "" {
		return s.pageBytes(ctx)
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(file.String + suffix)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// No log right now, which is the normal state just after a
				// checkpoint. Nothing to add, nothing wrong.
				continue
			}
			return 0, fmt.Errorf("store: volume: measure %s%s: %w", file.String, suffix, err)
		}
		total += fi.Size()
	}
	return total, nil
}

// pageBytes is what SQLite says the database occupies, used when there is no
// file to measure.
func (s *Store) pageBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("store: volume: page count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("store: volume: page size: %w", err)
	}
	return pageCount * pageSize, nil
}

// --- the free-space guard, which WB_GO does not have ---

// spaceFloorBytes is the absolute floor.
//
// The rule is the larger of this and two percent of the volume, because
// neither number works alone: a 256 MB floor on a 4 TB disk is no floor at
// all, and two percent of a 16 GB one is 320 MB.
const spaceFloorBytes = 256 << 20

// ErrDiskLow says collection must stop before it writes, rather than after
// SQLITE_FULL says so from the other side.
//
// It is raised where a person will meet it — a persistent banner and a
// collector-silent change once increment 4 exists — and not as a log line on a
// machine nobody has a terminal open to.
var ErrDiskLow = errors.New("store: not enough free space to collect")

// SetSpaceFloor overrides the absolute floor. Zero restores the default; it
// never means "no floor", for the same reason an unset retention never means
// unbounded.
func (s *Store) SetSpaceFloor(bytes int64) { s.floor.Store(bytes) }

func (s *Store) spaceFloor() int64 {
	if v := s.floor.Load(); v > 0 {
		return v
	}
	return spaceFloorBytes
}

// CheckSpace runs before each pass transaction.
func (s *Store) CheckSpace(_ context.Context) error {
	free, total, err := diskFree(s.path)
	if err != nil {
		// A filesystem that will not answer is not a reason to stop
		// collecting. Refusing to work because a statfs failed would be the
		// guard causing the outage it exists to prevent.
		return nil
	}
	floor := s.spaceFloor()
	if pct := total / 50; pct > floor {
		floor = pct
	}
	if free < floor {
		return fmt.Errorf("%w: %d MB free, floor is %d MB",
			ErrDiskLow, free>>20, floor>>20)
	}
	return nil
}
