// SPDX-License-Identifier: AGPL-3.0-or-later

// Command avitomon-tray is the same program with an icon.
//
// Built -H windowsgui so it has no console window: a monitor that opened a
// black rectangle at every login would be a monitor people turn off. It starts
// the panel and the collector exactly as `avitomon panel` does — the tray is an
// icon over the daemon and not a second implementation of it — and adds the two
// things a background process with no window otherwise cannot offer: a way to
// see it, and a way to stop it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/tray"
)

func main() {
	port := flag.Int("port", 8765, "порт панели")
	db := flag.String("db", "avito.db", "файл базы")
	flag.Parse()
	// The tray takes no command. Anything left over is somebody starting it the
	// way they start avitomon, and a tray that went on regardless would start a
	// panel child that is another tray, and so on without end.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "avitomon-tray не принимает команд (%q): это значок над «avitomon panel»\n", flag.Arg(0))
		os.Exit(2)
	}
	bin, err := panelBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// The panel runs as a child rather than in this process, and that is the
	// decision worth stating. -H windowsgui means no console, so anything the
	// panel prints — the address it came up on, a transport that would not
	// open — would go nowhere at all. A child keeps its own handles and can be
	// pointed at a log; and if the panel wedges, the icon is still answering
	// and can still stop it.
	panel := exec.CommandContext(ctx, bin, "panel",
		"-db", *db, "-port", fmt.Sprint(*port))
	panel.Stdout, panel.Stderr = os.Stdout, os.Stderr
	if err := panel.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "не удалось запустить панель:", err)
		os.Exit(1)
	}
	go func() {
		// The panel exiting is the whole product exiting. An icon that
		// outlived it would be an icon promising something that is gone.
		_ = panel.Wait()
		stop()
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", *port)
	// Away from Windows this answers ErrUnsupported straight away, which is
	// the right shape: the platform file says so once and this binary needs no
	// build tag of its own.
	err = tray.Run(ctx, tray.Config{
		Tip:       "Avito Monitor — " + url,
		OpenLabel: "Открыть панель",
		QuitLabel: "Выход",
		OnOpen:    func() { open(url) },
		OnQuit:    stop,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr,
			"Панель можно запускать напрямую: avitomon panel. "+
				"Юниты службы — в deploy/.")
		os.Exit(1)
	}
}

// panelBinary is the avitomon program lying next to this one. The panel is
// avitomon's command and not the tray's: the tray started as its own child
// was a tray starting trays, one more every time the last one came up.
func panelBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("не удалось найти себя: %w", err)
	}
	return siblingPanel(self, func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && !st.IsDir()
	})
}

// siblingPanel names avitomon in the folder of self, and refuses a path that
// is self or is missing.
func siblingPanel(self string, exists func(string) bool) (string, error) {
	name := "avitomon"
	if strings.EqualFold(filepath.Ext(self), ".exe") {
		name += ".exe"
	}
	bin := filepath.Join(filepath.Dir(self), name)
	if strings.EqualFold(filepath.Clean(bin), filepath.Clean(self)) {
		return "", fmt.Errorf("значок назван так же, как монитор (%s): переименуйте его в avitomon-tray", self)
	}
	if !exists(bin) {
		return "", fmt.Errorf("рядом со значком нет монитора: положите %s в %s", name, filepath.Dir(self))
	}
	return bin, nil
}

// open shows the panel in whatever the person uses to look at web pages.
func open(url string) {
	// rundll32 rather than `cmd /c start`, which would flash a console window
	// on a binary built to have none.
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
