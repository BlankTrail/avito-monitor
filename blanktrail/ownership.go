// SPDX-License-Identifier: AGPL-3.0-or-later

package blanktrail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// Lifted from Google_GO's internal/blanktrail/ownership.go, which met the
// problem first: a program stopped with Stop-Process -Force closes no pool, and
// every port it held stays open on the service. On 11 October 2026 the service
// on 192.168.0.1:8893 held sixty such ports from this program's own restarts.
//
// Every port is labelled «<program>-<installation>-<process>». At start an
// installation closes the ports carrying its own name and another process's —
// never this process's, never another installation's, never a port without a
// label. The program name is the caller's: this package must not name a site.

var processMark = func() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}()

var (
	ownerMu sync.Mutex
	program = "bt"
	owner   string
)

// SetOwner names the program and the installation every client made from here
// on labels its ports with.
func SetOwner(prog, installation string) {
	ownerMu.Lock()
	defer ownerMu.Unlock()
	if prog != "" {
		program = prog
	}
	owner = installation
}

// OwnerOf is an installation's name made from what tells it apart: the
// machine, and the history it keeps.
func OwnerOf(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:6])
}

func newLabel() string {
	ownerMu.Lock()
	defer ownerMu.Unlock()
	if owner == "" {
		return program + "-" + processMark
	}
	return program + "-" + owner + "-" + processMark
}

// Label is the mark this client puts on every port it opens.
func (c *Client) Label() string { return c.label }

// ReclaimLeftovers closes the ports an earlier run of this installation left
// on the service, and says how many.
func (c *Client) ReclaimLeftovers(ctx context.Context) (int, error) {
	ownerMu.Lock()
	o, prog := owner, program
	ownerMu.Unlock()
	if o == "" {
		return 0, nil
	}
	mine := prog + "-" + o + "-"
	current := prog + "-" + o + "-" + processMark
	var out struct {
		Ports []struct {
			Port  int    `json:"port"`
			Label string `json:"label"`
		} `json:"ports"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/ports", nil, &out); err != nil {
		return 0, err
	}
	closed := 0
	for _, p := range out.Ports {
		if !strings.HasPrefix(p.Label, mine) || p.Label == current {
			continue
		}
		if err := c.ClosePort(ctx, p.Port); err == nil {
			closed++
		}
	}
	return closed, nil
}
