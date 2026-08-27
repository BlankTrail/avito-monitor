// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

// Package tray is Windows-only. On every other platform the daemon is started
// by a service manager and there is no notification area to put an icon in —
// see deploy/ for the units.
package tray

import (
	"context"
	"errors"
)

// ErrUnsupported is what Run answers away from Windows.
//
// An error rather than a silent success, because a caller that asked for an
// icon and got nothing should be able to say so on the terminal rather than
// leave somebody looking for one.
var ErrUnsupported = errors.New("tray: значок в области уведомлений есть только в Windows")

// Config is the same shape everywhere so a caller needs no build tags.
type Config struct {
	Tip       string
	OpenLabel string
	QuitLabel string
	OnOpen    func()
	OnQuit    func()
}

// Run reports that there is nothing to run.
func Run(context.Context, Config) error { return ErrUnsupported }
