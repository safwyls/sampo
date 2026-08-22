# Static assets

`favicon.ico` is the companion's one piece of artwork, and deliberately
the only copy of it: Vite copies this folder into `dist/`, `embed.go`
embeds `dist/`, and the Windows tray reads the icon back out of that
embedded filesystem (`cmd/companion/tray_windows.go`). So the browser tab
and the tray icon cannot drift apart — there is nothing to keep in sync.

16×16 and 32×32 at 32bpp, which is what a tray wants; `.ico` is also what
browsers are happiest with at favicon sizes. Replacing it replaces both.
