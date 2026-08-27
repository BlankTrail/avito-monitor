// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package tray

import (
	"context"
	"testing"
	"time"
)

// TestTheIconGoesUpAndComesDown.
//
// The part of a tray that can be checked without eyes: the window class
// registers, Shell_NotifyIcon(NIM_ADD) succeeds, the message loop runs, a
// cancelled context reaches it, and NIM_DELETE takes the icon away. What no
// test here can check is that a person sees anything — this environment does
// not composite frames — so the pixels remain unverified and the measurement
// says so.
//
// It still catches the failures that actually happen: a struct laid out wrong,
// a flag that makes NIM_ADD return zero, a message loop that never wakes.
func TestTheIconGoesUpAndComesDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Tip:       "Avito Monitor — проверка",
			OpenLabel: "Открыть панель",
			QuitLabel: "Выход",
		})
	}()

	// Long enough for the window and the icon to exist; short enough that a
	// hang fails the test rather than the suite.
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the message loop did not return on a cancelled context")
	}
}

// TestQuitRunsItsCallbackBeforeReturning.
//
// «Выход» has to stop the daemon and not merely remove the icon: the whole
// reason the item exists is that a background process with no window cannot
// otherwise be stopped except through Task Manager.
func TestQuitRunsItsCallbackBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Tip: "проверка", OpenLabel: "Открыть", QuitLabel: "Выход",
			OnQuit: func() { close(quit) },
		})
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-quit:
	case <-time.After(10 * time.Second):
		t.Fatal("OnQuit was not called")
	}
	<-done
}

// TestALongTipIsTruncatedRatherThanOverrunning.
//
// szTip is a fixed 128 wide characters. A string that filled it with no room
// for the terminator is however many bytes of the adjacent struct Windows
// decides to render as a tooltip.
func TestALongTipIsTruncatedRatherThanOverrunning(t *testing.T) {
	var dst [128]uint16
	long := ""
	for i := 0; i < 400; i++ {
		long += "я"
	}
	copyTip(&dst, long)

	if dst[len(dst)-1] != 0 {
		t.Error("the tip filled its buffer with no terminator")
	}
	terminated := false
	for _, r := range dst {
		if r == 0 {
			terminated = true
			break
		}
	}
	if !terminated {
		t.Error("the tip has no NUL anywhere in it")
	}
}
