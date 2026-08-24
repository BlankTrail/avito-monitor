// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// This file is the egress configuration: where requests go out from.
//
// The tables are migration 0001's, lifted from the donor verbatim, and until
// now nothing wrote to them. Both transport modes have an egress, which is why
// the BlankTrail-versus-direct decision is a setting and not a channel kind.

// Channel is one way out to the network.
type Channel struct {
	ID   int64
	Name string
	// Kind is one of proxy-list, rotating, gateway, direct. The schema has the
	// CHECK; this type does not repeat it, because two places stating the
	// vocabulary is one place for them to disagree.
	Kind                 string
	Source               string
	RotateURL            string
	RotateMinIntervalSec int
	// Weight is the share of ports this channel gets, before the Mixer starts
	// demoting it for producing blocks. 100 is the default and means "an equal
	// share"; the number is relative to the other channels and not a percentage.
	Weight  int
	Enabled bool
}

// Proxy is one parsed entry of a list.
type Proxy struct {
	ID        int64
	ChannelID int64
	Scheme    string
	Host      string
	Port      int
	Username  string
	// Password is a credential and is never put into an error, a log or a
	// rendered page. The screen shows a host and a port.
	Password string
	Enabled  bool
}

const channelColumns = `id, name, kind, source, rotate_url,
	rotate_min_interval_sec, weight, enabled`

// Channels is every configured way out, in id order.
func (s *Store) Channels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+channelColumns+` FROM channels ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Source, &c.RotateURL,
			&c.RotateMinIntervalSec, &c.Weight, &c.Enabled); err != nil {
			return nil, fmt.Errorf("store: channels: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveChannel inserts or updates, keyed on the id.
//
// An update rather than a delete and an insert, because proxies.channel_id
// cascades: replacing the row would take every address with it, and a person
// changing a channel's weight would find their list empty.
func (s *Store) SaveChannel(ctx context.Context, c Channel) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now().UTC().Unix()
	if c.Weight <= 0 {
		c.Weight = 100
	}

	if c.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO channels
			  (name, kind, source, rotate_url, rotate_min_interval_sec,
			   weight, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Name, c.Kind, c.Source, c.RotateURL, c.RotateMinIntervalSec,
			c.Weight, c.Enabled, now, now)
		if err != nil {
			return 0, fmt.Errorf("store: save channel: %w", err)
		}
		return res.LastInsertId()
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE channels
		SET name = ?, kind = ?, source = ?, rotate_url = ?,
		    rotate_min_interval_sec = ?, weight = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		c.Name, c.Kind, c.Source, c.RotateURL, c.RotateMinIntervalSec,
		c.Weight, c.Enabled, now, c.ID); err != nil {
		return 0, fmt.Errorf("store: save channel %d: %w", c.ID, err)
	}
	return c.ID, nil
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete channel %d: %w", id, err)
	}
	return nil
}

// ReplaceProxies swaps a channel's whole address list, in one transaction.
//
// The whole list and not a merge: an address the provider removed must stop
// being used, and only replacing the set says that. One transaction because a
// refresh that failed halfway would leave the channel holding some of its
// addresses — a quiet halving of the budget that nothing would report.
//
// ON CONFLICT DO NOTHING and not INSERT OR IGNORE, and the difference is the
// whole reason this has a test. A provider's file repeats itself, and the
// address count is what the budget arithmetic multiplies — a duplicate counted
// twice doubles a person's stated ceiling and earns them a ban, so a repeat has
// to be tolerated. But INSERT OR IGNORE tolerates every constraint including
// the CHECK on the scheme, which would make an address this build cannot dial
// vanish from the list silently and leave the count on screen wrong in the
// other direction. DO NOTHING is scoped to the uniqueness conflict alone.
func (s *Store) ReplaceProxies(ctx context.Context, channelID int64, ps []Proxy) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM proxies WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO proxies
			  (channel_id, scheme, host, port, username, password, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (channel_id, scheme, host, port, username) DO NOTHING`,
			channelID, p.Scheme, p.Host, p.Port, p.Username, p.Password,
			p.Enabled); err != nil {
			// The address is named so somebody can find the line, and the
			// credential beside it is not: this goes to a log, a screen and
			// sometimes an issue report.
			return fmt.Errorf("store: replace proxies: %s:%d: %w", p.Host, p.Port, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	return nil
}

const proxyColumns = `id, channel_id, scheme, host, port, username, password, enabled`

func (s *Store) Proxies(ctx context.Context, channelID int64) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies WHERE channel_id = ? ORDER BY id`,
		channelID)
	if err != nil {
		return nil, fmt.Errorf("store: proxies: %w", err)
	}
	defer rows.Close()

	var out []Proxy
	for rows.Next() {
		var p Proxy
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Scheme, &p.Host, &p.Port,
			&p.Username, &p.Password, &p.Enabled); err != nil {
			return nil, fmt.Errorf("store: proxies: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountProxies is how many addresses each channel holds.
//
// All of them in one query, because the screen asks it of every row it draws
// and the budget arithmetic asks it of every enabled channel at once.
func (s *Store) CountProxies(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, count(*) FROM proxies WHERE enabled = 1 GROUP BY channel_id`)
	if err != nil {
		return nil, fmt.Errorf("store: count proxies: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: count proxies: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}
