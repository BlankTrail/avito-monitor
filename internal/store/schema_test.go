// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestEveryTableIsStrict. A non-STRICT table accepts a string into an INTEGER
// column and stores it, so a unit error or a nil that should have been a zero
// becomes a row nobody notices until a chart is wrong.
func TestEveryTableIsStrict(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(
		`SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		if name == "schema_migrations" {
			continue // the runner's own bookkeeping, lifted as it stands
		}
		n++
		if !strings.Contains(strings.ToUpper(ddl), "STRICT") {
			t.Errorf("table %s is not STRICT", name)
		}
	}
	if n != 21 {
		t.Errorf("found %d tables, want the 21 the spec's migration 0001 defines", n)
	}
}

// TestNoOnDeleteRestrict. The emergency reclaim deletes in a fixed order, and
// a RESTRICT anywhere would let a cascade deadlock on its own foreign keys at
// exactly the moment the disk is full.
func TestNoOnDeleteRestrict(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(`SELECT name, sql FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToUpper(ddl), "ON DELETE RESTRICT") {
			t.Errorf("table %s uses ON DELETE RESTRICT", name)
		}
	}
}

// millisecondDurations are the columns that measure how long something took.
//
// They are exempt because the rule is about points in time, not about spans. A
// duration has no epoch and is never bucketed, so it never meets the retention
// arithmetic that the rule exists to protect. The first draft of this test did
// not draw that distinction and failed on elapsed_ms, which was the test being
// wrong rather than the schema.
var millisecondDurations = map[string]bool{
	"elapsed_ms": true,
}

// TestOnlyTwoTimestampsAreMilliseconds. Every point in time in this schema is
// whole Unix seconds except Avito's own two, which are kept as sent. A third
// would make the retention arithmetic — written in 86400 and 604800 literals —
// silently wrong for that column, and a wrongly rescaled bucket fails as a
// wrong chart and never as a compile error.
func TestOnlyTwoTimestampsAreMilliseconds(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(
		`SELECT m.name, p.name FROM sqlite_master m
		 JOIN pragma_table_info(m.name) p
		 WHERE m.type='table' AND p.name LIKE '%\_ms' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		if millisecondDurations[col] {
			continue
		}
		got[col] = true
	}
	for _, want := range []string{"sort_ts_ms", "allow_ts_ms"} {
		if !got[want] {
			t.Errorf("column %s is missing", want)
		}
		delete(got, want)
	}
	for col := range got {
		t.Errorf("unexpected millisecond timestamp %q — every other point in "+
			"time in this schema is whole seconds, and the retention "+
			"arithmetic assumes it. If this is a duration rather than a "+
			"moment, add it to millisecondDurations and say why", col)
	}
}

// TestPriceKindIsThreeValuedEverywhereItAppears. A null price is ambiguous
// between «Цена договорная», which is the seller's own statement and a real
// event, and a parse gap, which is our defect. Collapsing them means that the
// first time Avito withholds the hydration state, thirty listings
// simultaneously "become negotiable".
func TestPriceKindIsThreeValuedEverywhereItAppears(t *testing.T) {
	s := openTemp(t)
	var ddl string
	if err := s.DB().QueryRow(
		`SELECT sql FROM sqlite_master WHERE name='listing_snapshots'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"'exact'", "'negotiable'", "'absent'"} {
		if !strings.Contains(ddl, kind) {
			t.Errorf("listing_snapshots.price_kind does not admit %s", kind)
		}
	}
}
