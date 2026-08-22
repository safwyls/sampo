package main

// The Windows executable's own icon.
//
// rsrc_windows_amd64.syso in this directory is a COFF resource object the
// Go linker picks up automatically for GOOS=windows: it carries the same
// artwork as the tray and the page's favicon, so Explorer, the taskbar,
// the Alt-Tab switcher and the download in a browser's shelf all show one
// icon rather than the default blank page.
//
// It is a *derived* file — generated from web/companion/public/favicon.ico,
// which stays the single source of the artwork. Committing it means a
// plain `go build` produces a properly-iconed exe with no toolchain
// beyond Go; the cost is that it can go stale when the icon changes, so
// CI regenerates it and fails if the bytes differ (rsrc's output is
// deterministic). To refresh it by hand after editing the icon:
//
//	go generate ./cmd/companion
//
//go:generate go run github.com/akavel/rsrc@v0.10.2 -ico ../../web/companion/public/favicon.ico -arch amd64 -o rsrc_windows_amd64.syso
