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

	"github.com/BlankTrail/avito-monitor/internal/tray"
)

func main() {
	port := flag.Int("port", 8765, "порт панели")
	db := flag.String("db", "avito.db", "файл базы")
	flag.Parse()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// The panel runs as a child rather than in this process, and that is the
	// decision worth stating. -H windowsgui means no console, so anything the
	// panel prints — the address it came up on, a transport that would not
	// open — would go nowhere at all. A child keeps its own handles and can be
	// pointed at a log; and if the panel wedges, the icon is still answering
	// and can still stop it.
	panel := exec.CommandContext(ctx, self(), "panel",
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
	err := tray.Run(ctx, tray.Config{
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

// self is this binary's path, so the child is the same build.
func self() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "avitomon"
}

// open shows the panel in whatever the person uses to look at web pages.
func open(url string) {
	// rundll32 rather than `cmd /c start`, which would flash a console window
	// on a binary built to have none.
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
