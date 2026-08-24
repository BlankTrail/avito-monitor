// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// PacerConfig is the two budgets and the clock.
type PacerConfig struct {
	// Floor is the minimum gap between two requests on the SAME identity.
	// 3.5 seconds: the measurement was forty of forty at three-second spacing,
	// and a default should not sit on the edge of what worked.
	Floor time.Duration
	// Jitter is the fraction either side of the floor. 0.2 in production, zero
	// in a test that wants a number it can assert on. A metronome is itself a
	// signal.
	Jitter float64
	// Ceiling and Window are the rolling cap per EGRESS ADDRESS: sixty per ten
	// minutes, a third under the ninety that was observed to harden a 429 into
	// a 403.
	Ceiling int
	Window  time.Duration

	Now        func() time.Time
	SleepUntil func(context.Context, time.Time) error
}

// Budget is the arithmetic, published rather than documented, because the
// answer moves with the configuration and the panel refuses a schedule with it.
type Budget struct {
	PerAddressPerHour    int
	Addresses            int
	WatchesAtFiveMinutes int
}

// Pacer holds a floor per identity and a ceiling per address.
//
// Two budgets, because identities and addresses are exhausted by different
// things. A pool of ports multiplies identities; it does not multiply the exit
// address, and Avito's reported /24-level aggregation means the address has a
// ceiling of its own that more identities do not raise. Give each port its own
// upstream and the second budget multiplies with the addresses; run them all
// direct and it does not — and the panel says which, because "I added ports and
// it got banned faster" is otherwise an unanswerable support question.
type Pacer struct {
	mu        sync.Mutex
	cfg       PacerConfig
	lastSeen  map[string]time.Time   // identity -> when it last went out
	windows   map[string][]time.Time // egress -> the requests inside the window
	addresses int
}

// PacerState is the persistable form. Increment 3 moves it into
// transport_sessions and egress_windows; here it is one file.
type PacerState struct {
	LastSeen map[string]time.Time   `json:"last_seen"`
	Windows  map[string][]time.Time `json:"windows"`
}

// DefaultFloor and the ceiling constants are the measurements, named so that a
// reader meets the number and its reason together.
const (
	DefaultFloor   = 3500 * time.Millisecond
	DefaultJitter  = 0.2
	DefaultCeiling = 60
	DefaultWindow  = 10 * time.Minute
)

func NewPacer(cfg PacerConfig) *Pacer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SleepUntil == nil {
		cfg.SleepUntil = sleepUntil
	}
	if cfg.Floor <= 0 {
		cfg.Floor = DefaultFloor
	}
	if cfg.Ceiling <= 0 {
		cfg.Ceiling = DefaultCeiling
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	return &Pacer{
		cfg:       cfg,
		lastSeen:  map[string]time.Time{},
		windows:   map[string][]time.Time{},
		addresses: 1,
	}
}

// SetAddresses records how many distinct egresses are configured, for Budget.
func (p *Pacer) SetAddresses(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 {
		n = 1
	}
	p.addresses = n
}

// Wait blocks until this identity may go out again on this address, then
// records the request against both budgets.
func (p *Pacer) Wait(ctx context.Context, identity, egress string) error {
	for {
		p.mu.Lock()
		now := p.cfg.Now()
		until := now

		if last, ok := p.lastSeen[identity]; ok {
			if t := last.Add(p.gap()); t.After(until) {
				until = t
			}
		}
		hits := p.recent(egress, now)
		if len(hits) >= p.cfg.Ceiling {
			// The oldest request in the window has to fall out of it before
			// another may go.
			if t := hits[0].Add(p.cfg.Window); t.After(until) {
				until = t
			}
		}
		if !until.After(now) {
			p.lastSeen[identity] = now
			p.windows[egress] = append(hits, now)
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		if err := p.cfg.SleepUntil(ctx, until); err != nil {
			return err
		}
	}
}

// gap is the floor with jitter.
func (p *Pacer) gap() time.Duration {
	if p.cfg.Jitter <= 0 {
		return p.cfg.Floor
	}
	span := float64(p.cfg.Floor) * p.cfg.Jitter
	return p.cfg.Floor + time.Duration((rand.Float64()*2-1)*span)
}

// recent drops requests that have fallen out of the window. Caller holds mu.
func (p *Pacer) recent(egress string, now time.Time) []time.Time {
	var out []time.Time
	for _, t := range p.windows[egress] {
		if now.Sub(t) < p.cfg.Window {
			out = append(out, t)
		}
	}
	p.windows[egress] = out
	return out
}

// Budget publishes the arithmetic for the configuration in use. One page-one
// pass is one request, and a five-minute interval is twelve passes an hour.
func (p *Pacer) Budget() Budget {
	p.mu.Lock()
	defer p.mu.Unlock()
	perHour := int(float64(p.cfg.Ceiling) * float64(time.Hour) / float64(p.cfg.Window))
	return Budget{
		PerAddressPerHour:    perHour,
		Addresses:            p.addresses,
		WatchesAtFiveMinutes: perHour / 12 * p.addresses,
	}
}

func (p *Pacer) Snapshot() PacerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	ls := make(map[string]time.Time, len(p.lastSeen))
	for k, v := range p.lastSeen {
		ls[k] = v
	}
	w := make(map[string][]time.Time, len(p.windows))
	for k, v := range p.windows {
		w[k] = append([]time.Time(nil), v...)
	}
	return PacerState{LastSeen: ls, Windows: w}
}

// Restore puts a saved state back. Without it, restarting buys sixty more
// requests on every address — and the budget exists precisely because
// exceeding it hardens a 429 into a 403.
func (p *Pacer) Restore(s PacerState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range s.LastSeen {
		p.lastSeen[k] = v
	}
	for k, v := range s.Windows {
		p.windows[k] = append([]time.Time(nil), v...)
	}
}

func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
