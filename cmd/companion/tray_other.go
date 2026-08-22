//go:build !windows

package main

import "os"

// runUI on non-Windows platforms is a plain foreground process: the game
// client only exists on Windows, so anything else running this is a
// developer with a terminal.
func runUI(a *app, url string) {
	select {}
}

// exitForRestart ends this process so the replacement started by
// restartSelf takes over. Nothing to tear down here.
func exitForRestart() { os.Exit(0) }
