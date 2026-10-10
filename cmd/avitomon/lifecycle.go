// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// The collector used to be opened once, when the process started, and never
// again.
//
// Found on 11 October 2026 by setting up an installation the way the panel
// invites: choose BlankTrail, paste the key, add exits. The process had started
// before any of that, so its collector had failed on «needs a key», printed
// «поправьте настройки и перезапустите» to a terminal nobody was looking at,
// and stopped — while /healthz answered «ok: собирает 5 из 5» and the watches
// screen called every watch «идёт». Nothing was collected and everything said
// otherwise. The same was true, silently, of every exit added after start.
//
// Now a supervisor owns it: it opens the collector, and whenever what the
// transport is built from changes — the mode, the BlankTrail address, key or
// ports, the exits and their addresses — it closes it and opens a new one. A
// collector that will not open is a state the panel shows, with its reason,
// and it is retried on the next change rather than never.

// collectorState is what the panel and /healthz are told.
type collectorState struct {
	mu      sync.Mutex
	running bool
	why     string
	since   time.Time
}

func (c *collectorState) set(running bool, why string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running != running || c.why != why {
		c.since = time.Now()
	}
	c.running, c.why = running, why
}

// State reports whether collection runs in this process, and why not.
func (c *collectorState) State() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running, c.why
}

// rebuildPoll is how often the supervisor looks for a change. Ten seconds is
// below what a person notices after pressing «Сохранить», and a fingerprint is
// a handful of reads.
const rebuildPoll = 10 * time.Second

// superviseCollection runs until ctx ends.
func superviseCollection(ctx context.Context, s *store.Store, opts leaserOpts, st *collectorState) {
	for ctx.Err() == nil {
		fp, err := transportFingerprint(ctx, s)
		if err != nil {
			st.set(false, err.Error())
			if !sleepCtx(ctx, rebuildPoll) {
				return
			}
			continue
		}

		c, err := openCollector(ctx, s, opts)
		if err != nil {
			st.set(false, err.Error())
			fmt.Println("Сбор не запустился:", err)
			// Wait for something to change rather than hammering the same
			// failure: a wrong key does not become right on its own.
			waitForChange(ctx, s, fp)
			continue
		}

		run, stop := context.WithCancel(ctx)
		startCollecting(run, s, c)
		st.set(true, "")
		fmt.Println("Сбор идёт в этом же процессе.")

		waitForChange(ctx, s, fp)
		stop()
		// The workers notice the cancel between turns; give a pass in flight
		// a moment to write itself before the transport goes away under it.
		sleepCtx(context.Background(), 2*time.Second)
		_ = c.Close()
		if ctx.Err() == nil {
			fmt.Println("Настройки выхода изменились — сбор перезапускается.")
		}
	}
}

func waitForChange(ctx context.Context, s *store.Store, was string) {
	for sleepCtx(ctx, rebuildPoll) {
		now, err := transportFingerprint(ctx, s)
		if err == nil && now != was {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// transportFingerprint is everything the transport is built from, hashed, so a
// change to any of it is a change — and the key is in the hash, never in a log.
func transportFingerprint(ctx context.Context, s *store.Store) (string, error) {
	var parts []string
	mode, err := s.TransportMode(ctx)
	if err != nil && !errors.Is(err, store.ErrNoTransportChosen) {
		return "", err
	}
	parts = append(parts, "mode="+string(mode))
	if cfg, err := s.BlankTrailConfig(ctx); err == nil {
		parts = append(parts, fmt.Sprintf("bt=%s|%s|%d", cfg.ControlURL, cfg.APIKey, cfg.Ports))
	}
	chans, err := s.Channels(ctx)
	if err != nil {
		return "", err
	}
	counts, _ := s.CountProxies(ctx)
	for _, c := range chans {
		parts = append(parts, fmt.Sprintf("ch=%d|%s|%s|%s|%s|%t|%d",
			c.ID, c.Kind, c.Name, c.Source, c.RotateURL, c.Enabled, counts[c.ID]))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
