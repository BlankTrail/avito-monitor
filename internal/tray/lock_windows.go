// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package tray

import "runtime"

// A window belongs to the thread that created it, and only that thread may
// pump its messages. Wrapped in two named functions rather than called inline
// so that the reason has somewhere to live.
func lockOSThread()   { runtime.LockOSThread() }
func unlockOSThread() { runtime.UnlockOSThread() }
