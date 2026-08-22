//go:build windows

package main

import (
	"io/fs"
	"log"
	"os"
	"time"

	"fyne.io/systray"

	web "github.com/safwyls/artificer/web/companion"
)

// trayIcon is the same favicon.ico the page shows in its tab: one piece
// of artwork, embedded once with the frontend (web/companion/public),
// so the tray and the browser tab cannot drift apart.
func trayIcon() []byte {
	dist, err := web.Dist()
	if err != nil {
		return nil
	}
	data, err := fs.ReadFile(dist, "favicon.ico")
	if err != nil {
		// A tray with no icon is still a working tray; the menu is the
		// part that matters.
		log.Printf("tray icon: %v", err)
		return nil
	}
	return data
}

// exitForRestart ends this process so the replacement started by
// restartSelf takes over. The tray icon has to go first: systray owns a
// window and a shell notification-area entry, and a process that exits
// without giving it up leaves a ghost icon behind until someone hovers
// over it.
func exitForRestart() {
	systray.Quit()
	// systray.Quit returns before the icon is actually gone; give the
	// shell a moment rather than racing it.
	time.Sleep(250 * time.Millisecond)
	os.Exit(0)
}

// runUI parks the companion in the system tray: open the page, sync on
// demand, read the custody state at a glance, quit. The page is the UI —
// the tray is the handle.
func runUI(a *app, url string) {
	// A console-subsystem build (plain `go build`) double-clicked from
	// Explorer drags a console window along; close it once startup has
	// printed the URL. See console_windows.go.
	detachOwnConsole()
	systray.Run(func() {
		if icon := trayIcon(); icon != nil {
			systray.SetIcon(icon)
		}
		systray.SetTooltip("Artificer Companion")

		open := systray.AddMenuItem("Open companion page", "Your shared worlds, in the browser")
		syncNow := systray.AddMenuItem("Sync now", "Poll the service and push any due checkpoints")
		systray.AddSeparator()
		status := systray.AddMenuItem("Starting…", "Custody state")
		status.Disable()
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Stop syncing")

		// The status line follows the app state; a menu the player only
		// glances at occasionally doesn't need to be fresher than this.
		ticker := time.NewTicker(5 * time.Second)
		update := func() { status.SetTitle(a.statusLine()) }
		update()

		go func() {
			for {
				select {
				case <-open.ClickedCh:
					openBrowser(url)
				case <-syncNow.ClickedCh:
					go func() {
						if a.syncConfigured() {
							a.syncRefresh()
							for _, id := range a.linkedWorldIDs() {
								a.adoptHandoff(id)
								a.autoCheckpoint(id)
							}
						} else {
							openBrowser(url) // nothing to sync: the setup lives on the page
						}
						update()
					}()
				case <-ticker.C:
					update()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, nil)
}
