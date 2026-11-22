// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSiblingPanel_IsAvitomonNextToTheTray(t *testing.T) {
	dir := filepath.Join("C:", "Программы", "BlankTrail")
	got, err := siblingPanel(filepath.Join(dir, "avitomon-tray.exe"), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "avitomon.exe"); got != want {
		t.Errorf("panel binary = %q, want %q", got, want)
	}
}

func TestSiblingPanel_NeverTheTrayItself(t *testing.T) {
	// The bug this replaces: the tray started itself with «panel», did not
	// understand it, and started another tray.
	self := filepath.Join("opt", "avitomon")
	if _, err := siblingPanel(self, func(string) bool { return true }); err == nil {
		t.Error("a tray named avitomon was allowed to start itself")
	}
}

func TestSiblingPanel_SaysWhatIsMissing(t *testing.T) {
	_, err := siblingPanel(filepath.Join("opt", "avitomon-tray"), func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "avitomon") {
		t.Errorf("err = %v, want it to name the missing program", err)
	}
}
