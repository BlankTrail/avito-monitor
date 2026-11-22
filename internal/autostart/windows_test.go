// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package autostart

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestCommandLine_QuotesThePathAlways(t *testing.T) {
	// The default install path has two spaces in it. Unquoted, Windows reads
	// "C:\Program Files\BlankTrail\avitomon.exe" as the command "C:\Program.exe"
	// with "Files\BlankTrail\avitomon.exe" as an argument — which is not only
	// broken but the classic unquoted-path weakness: anything that can write
	// C:\Program.exe gets run at every login.
	got := commandLine(Command{
		Exe:  `C:\Program Files\BlankTrail\avitomon.exe`,
		Args: []string{"-open=false"},
	})
	if !strings.HasPrefix(got, `"C:\Program Files\BlankTrail\avitomon.exe"`) {
		t.Errorf("command line = %q, want the path quoted", got)
	}
	if !strings.HasSuffix(got, "-open=false") {
		t.Errorf("command line = %q, want the flags after the path", got)
	}
}

func TestCommandLine_QuotesEvenAPathWithoutSpaces(t *testing.T) {
	// Quoting only when a space is present makes the rule depend on where the
	// user installed, which is exactly the case nobody tests.
	got := commandLine(Command{Exe: `C:\avitomon\avitomon.exe`})
	if !strings.HasPrefix(got, `"`) {
		t.Errorf("command line = %q, want it quoted unconditionally", got)
	}
}

// TestTheEntryRoundTripsThroughTheRealRegistry.
//
// Enable, Disable and Enabled were the three functions in this package that
// touch the world, and none of them had a test: only commandLine did. The
// panel's «Запускать при входе в систему» was a tick nobody had ever watched
// reach the registry.
//
// Pointed at a key of this package's own rather than at Run. A test that could
// leave a value behind under Run would name an executable that no longer exists
// and try to start it at every login, for ever, on the machine of whoever ran
// the suite.
func TestTheEntryRoundTripsThroughTheRealRegistry(t *testing.T) {
	oldKey, oldName := runKey, entryName
	runKey, entryName = `Software\BlankTrail\avitomon-test`, "entry"
	t.Cleanup(func() {
		_ = Disable()
		registry.DeleteKey(registry.CURRENT_USER, runKey)
		runKey, entryName = oldKey, oldName
	})

	// Absent before anything is written, and not an error: the key does not
	// exist yet, and «is it on» has an answer even then.
	if on, err := Enabled(); err != nil || on {
		t.Fatalf("до записи: %v, %v", on, err)
	}

	// This test binary's own path: Command.Validate refuses an executable that
	// is not there, which is the safeguard that stops the panel writing a Run
	// entry pointing at nothing — so the test has to hand it something real.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Exe: exe, Args: []string{"panel"}}
	if err := Enable(cmd); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(); err != nil || !on {
		t.Fatalf("после записи: %v, %v", on, err)
	}

	// The value is the quoted command line and not something else that happened
	// to be truthy.
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := k.GetStringValue(entryName)
	_ = k.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := commandLine(cmd); got != want {
		t.Errorf("в реестре %q, ожидалось %q", got, want)
	}

	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(); err != nil || on {
		t.Fatalf("после снятия: %v, %v", on, err)
	}
	// Twice, because «убедись, что выключено» is the request and it is already
	// off — the second call must not become an error.
	if err := Disable(); err != nil {
		t.Errorf("повторное снятие: %v", err)
	}
}

func TestCommandLine_QuotesAnArgumentWithASpace(t *testing.T) {
	// The entry names the database by its full path, and a profile folder
	// can have a space in it. Unquoted, Windows hands the panel two arguments
	// and the panel opens a database called «C:\Users\Иван».
	got := commandLine(Command{
		Exe:  `C:\avitomon\avitomon.exe`,
		Args: []string{"panel", "-db", `C:\Users\Иван Петров\avito.db`},
	})
	want := `"C:\avitomon\avitomon.exe" panel -db "C:\Users\Иван Петров\avito.db"`
	if got != want {
		t.Errorf("command line = %q, want %q", got, want)
	}
}
