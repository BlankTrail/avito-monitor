// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package autostart

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

func isAbs(p string) bool                           { return filepath.IsAbs(p) }
func filepathEvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

// runKey is where Windows looks for what to start at login, and entryName is
// what our value is called under it.
//
// HKCU, not HKLM: a monitor is one person's tool watching one person's
// listings, and the machine-wide key would need administrator rights to solve
// a problem nobody has.
//
// Variables rather than constants so a test can round-trip these three
// functions against the real registry — CreateKey, SetStringValue, DeleteValue
// and GetStringValue as they are actually called — while pointing at a key of
// its own. Nothing about this test may be able to leave a value behind under
// Run: a stray entry there names an executable that no longer exists and tries
// to start it at every login, for ever, on the machine of whoever ran the
// tests.
var (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	entryName = Name
)

// Enable writes the entry.
func Enable(c Command) error {
	if err := c.Validate(); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()

	if err := k.SetStringValue(entryName, commandLine(c)); err != nil {
		return fmt.Errorf("autostart: writing the Run entry: %w", err)
	}
	return nil
}

// Disable removes it.
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()

	// A missing value is not an error: "make sure this is off" is the request,
	// and it is already off.
	if err := k.DeleteValue(entryName); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("autostart: removing the Run entry: %w", err)
	}
	return nil
}

// Enabled reports whether the entry is there.
func Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()

	if _, _, err := k.GetStringValue(entryName); err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("autostart: reading the Run entry: %w", err)
	}
	return true, nil
}

// commandLine renders the value Windows will run.
//
// The path is quoted, always. An unquoted path with a space in it — and the
// default install path has two — is read by Windows as a command followed by
// arguments, so "C:\Program Files\BlankTrail\avitomon.exe" starts
// "C:\Program.exe" with "Files\BlankTrail\avitomon.exe" as an argument. That is
// not only broken, it is the classic unquoted-service-path weakness.
func commandLine(c Command) string {
	var b strings.Builder
	b.WriteString(`"` + c.Exe + `"`)
	// Arguments go through the same rules the process will parse them by: the
	// database is named by its full path, and a profile folder has spaces.
	for _, a := range c.Args {
		b.WriteString(" " + syscall.EscapeArg(a))
	}
	return b.String()
}
