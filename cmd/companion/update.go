package main

// In-place updates from GitHub releases.
//
// The companion is one exe a player downloaded once and will never think
// about again, which is exactly the shape that ends up months behind the
// service it talks to. So it asks GitHub what the current build is, says
// so on its page, and can replace itself with one click.
//
// **Identity, not ordering.** Every release stamps main.version with a
// 12-character commit SHA — tagged releases too — and SHAs cannot be
// compared for "newer". So the question this asks is not "is theirs
// greater than mine" but "is theirs the one I am running": the release
// publishes companion-version.txt saying which build it is, and a
// companion whose own stamp differs is not running it. That is weaker
// than semver in one way (it cannot tell forward from backward) and
// stronger in another (it cannot be fooled by a version string that
// lies about what was built).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// updateRepo is where releases come from. Overridable so a fork
	// updates from itself rather than from upstream.
	defaultUpdateRepo = "safwyls/artificer"
	// updateTag is the rolling release every push to main republishes.
	// Tagged releases exist too, but this is the one that is always the
	// current build.
	updateTag = "companion-latest"
	// updateCheckEvery paces the background check. The answer changes
	// when someone pushes to main, so this is generous; unauthenticated
	// GitHub allows 60 requests an hour and this uses two.
	updateCheckEvery = 6 * time.Hour
	// The largest download this will accept, a sanity bound rather than
	// a real limit — the exe is around 12 MB.
	maxUpdateBytes = 200 << 20
)

// updateState is what the page and the tray know about updates.
type updateState struct {
	// Available is true when the release names a build that is not this
	// one. Deliberately not "newer": see the note above.
	Available bool `json:"available"`
	// Version is the release's build stamp, empty when unknown.
	Version string `json:"version,omitempty"`
	// CheckedAt is when GitHub last answered.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Error is why the last check or apply failed. Updates are a
	// convenience; a failure here never touches custody.
	Error string `json:"error,omitempty"`
	// Applying is set while a replacement is in flight.
	Applying bool `json:"applying,omitempty"`
	// Supported is false when this build cannot replace itself — a
	// `go run` with no real executable path, or an install the player
	// cannot write to (Program Files without elevation). The page says
	// so rather than offering a button that will fail.
	Supported bool   `json:"supported"`
	Why       string `json:"why,omitempty"`
}

func updateRepo() string {
	if r := strings.TrimSpace(os.Getenv("COMPANION_UPDATE_REPO")); r != "" {
		return r
	}
	return defaultUpdateRepo
}

// updateAssetName is the release asset for this platform. The names are
// frozen: players hold links to them.
func updateAssetName() string {
	switch runtime.GOOS {
	case "windows":
		return "artificer-companion.exe"
	case "linux":
		return "artificer-companion-linux"
	}
	return ""
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func (r ghRelease) asset(name string) (ghAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

// checkUpdate asks GitHub which build the rolling release ships and
// records whether it is this one. Errors are recorded, never returned to
// a caller that would treat them as fatal: not knowing about an update
// is not a problem with the companion.
func (a *app) checkUpdate(ctx context.Context) {
	st, err := a.fetchUpdateStateFrom(ctx, a.releaseAPIBase())
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	st.CheckedAt = &now
	st.Supported, st.Why = a.canSelfUpdateLocked()
	if err != nil {
		st.Error = err.Error()
	}
	a.update = st
}

// releaseAPIBase is the repository's release API root. Split out so a
// test can point the whole flow at a stand-in GitHub.
func (a *app) releaseAPIBase() string {
	return "https://api.github.com/repos/" + updateRepo()
}

func (a *app) fetchUpdateStateFrom(ctx context.Context, base string) (updateState, error) {
	var st updateState
	asset := updateAssetName()
	if asset == "" {
		return st, fmt.Errorf("no release is published for %s", runtime.GOOS)
	}
	rel, err := a.fetchRelease(ctx, base)
	if err != nil {
		return st, err
	}
	verAsset, ok := rel.asset("companion-version.txt")
	if !ok {
		// Releases published before this feature existed do not say
		// which build they are. Saying nothing beats guessing — the next
		// release carries the file and this starts working.
		return st, errors.New("the latest release does not say which build it is; it predates in-place updates")
	}
	remote, err := a.fetchText(ctx, verAsset.URL, 128)
	if err != nil {
		return st, err
	}
	st.Version = strings.TrimSpace(remote)
	if st.Version == "" {
		return st, errors.New("the latest release published an empty version")
	}
	if _, ok := rel.asset(asset); !ok {
		return st, fmt.Errorf("the latest release has no %s", asset)
	}
	// A development build ("dev", a plain `go build`) is not something
	// anyone wants replaced out from under them by a release.
	st.Available = version != "dev" && st.Version != version
	return st, nil
}

func (a *app) fetchRelease(ctx context.Context, base string) (ghRelease, error) {
	var rel ghRelease
	url := fmt.Sprintf("%s/releases/tags/%s", strings.TrimRight(base, "/"), updateTag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := a.client.Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return rel, errors.New("GitHub is rate-limiting this machine; the next check will try again")
	}
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("GitHub answered %d looking for the %s release", resp.StatusCode, updateTag)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return rel, fmt.Errorf("reading the release: %w", err)
	}
	return rel, nil
}

func (a *app) fetchText(ctx context.Context, url string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s answered %d", filepath.Base(url), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return string(body), err
}

// canSelfUpdateLocked reports whether this install can replace itself,
// and why not when it cannot. Checked before offering the button rather
// than after pressing it.
func (a *app) canSelfUpdateLocked() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, "this build has no executable to replace"
	}
	if updateAssetName() == "" {
		return false, "no release is published for " + runtime.GOOS
	}
	// Replacing means writing a new file into the exe's own directory
	// and renaming over it, so that directory has to be writable. A
	// companion in Program Files is not, without elevation.
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".companion-write-probe-*")
	if err != nil {
		return false, "this companion lives somewhere it cannot write to (" + dir + ") — move it, or replace it by hand"
	}
	probe.Close()
	os.Remove(probe.Name())
	return true, ""
}

// applyUpdate downloads the release's binary, verifies it against the
// release's checksum, and swaps it in.
//
// The swap is a rename dance, because a running executable cannot be
// overwritten on Windows — but it *can* be renamed. Current binary moves
// aside to .old, the download takes its place, and the .old is cleared
// out on the next start. Both files live in the same directory so the
// renames stay on one volume and cannot half-happen; if the second
// rename fails the first is undone, so a failed update leaves the
// working companion exactly where it was.
func (a *app) applyUpdate(ctx context.Context) error {
	a.mu.Lock()
	if a.worldSync.Busy {
		a.mu.Unlock()
		// Replacing the binary mid-transfer would kill a save in flight.
		return errors.New("a save transfer is running — try again once it finishes")
	}
	if a.update.Applying {
		a.mu.Unlock()
		return errors.New("an update is already being applied")
	}
	ok, why := a.canSelfUpdateLocked()
	if !ok {
		a.mu.Unlock()
		return errors.New(why)
	}
	a.update.Applying = true
	a.mu.Unlock()

	err := a.doApplyUpdate(ctx)

	a.mu.Lock()
	a.update.Applying = false
	if err != nil {
		a.update.Error = err.Error()
	}
	a.mu.Unlock()
	return err
}

func (a *app) doApplyUpdate(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The exe may be reached through a symlink; the rename dance has to
	// operate on the real file, in the real directory.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return a.swapInUpdateFrom(ctx, a.releaseAPIBase(), exe)
}

// swapInUpdateFrom downloads the release's binary for this platform,
// verifies it, and puts it at exe. Takes both the API root and the
// target path so a test can exercise the whole thing without a network
// or a real installed companion.
func (a *app) swapInUpdateFrom(ctx context.Context, base, exe string) error {
	rel, err := a.fetchRelease(ctx, base)
	if err != nil {
		return err
	}
	name := updateAssetName()
	asset, ok := rel.asset(name)
	if !ok {
		return fmt.Errorf("the latest release has no %s", name)
	}
	want, err := a.releaseChecksum(ctx, rel, name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)

	tmp, err := os.CreateTemp(dir, ".companion-update-*")
	if err != nil {
		return fmt.Errorf("making room for the download: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has moved it

	sum, size, err := a.downloadTo(ctx, tmp, asset.URL)
	tmp.Close()
	if err != nil {
		return err
	}
	if sum != want {
		return fmt.Errorf("the download does not match the release's checksum (%d bytes) — nothing was replaced", size)
	}
	if err := verifyExecutable(tmpName); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	// The dance. Aside, then into place, then undo if the second failed.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("moving the running companion aside: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		if undo := os.Rename(old, exe); undo != nil {
			// Both renames failed: say where the working binary is,
			// because the obvious next step is to put it back by hand.
			return fmt.Errorf("the update failed and the old companion is at %s — rename it back: %w", old, err)
		}
		return fmt.Errorf("putting the new companion in place: %w", err)
	}
	return nil
}

// releaseChecksum pulls this asset's expected SHA-256 out of the
// release's checksum manifest (the `sha256sum` format: hash, spaces,
// filename, one per line).
func (a *app) releaseChecksum(ctx context.Context, rel ghRelease, asset string) (string, error) {
	manifest, ok := rel.asset("companion-sha256.txt")
	if !ok {
		return "", errors.New("the latest release publishes no checksums, so a download cannot be verified")
	}
	body, err := a.fetchText(ctx, manifest.URL, 64<<10)
	if err != nil {
		return "", err
	}
	if sum := checksumFor(body, asset); sum != "" {
		return sum, nil
	}
	return "", fmt.Errorf("the release's checksums do not cover %s", asset)
}

// checksumFor reads one entry out of a sha256sum manifest.
func checksumFor(manifest, asset string) string {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum writes "*name" for binary mode.
		if strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) == 64 {
			return sum
		}
	}
	return ""
}

func (a *app) downloadTo(ctx context.Context, dst io.Writer, url string) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("downloading the update answered %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(resp.Body, maxUpdateBytes))
	if err != nil {
		return "", n, fmt.Errorf("downloading the update: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// verifyExecutable is a shape check on what was downloaded, so a proxy's
// error page or an HTML interstitial cannot be renamed over a working
// companion. The checksum already caught that; this catches a release
// that shipped the wrong file.
func verifyExecutable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return errors.New("the download is too small to be a companion")
	}
	switch runtime.GOOS {
	case "windows":
		if string(head[:2]) != "MZ" {
			return errors.New("the download is not a Windows executable")
		}
	case "linux":
		if string(head) != "\x7fELF" {
			return errors.New("the download is not a Linux executable")
		}
	}
	return nil
}

// clearOldBinary removes the previous build left beside this one by an
// update. Called at startup, which is the first moment it is no longer
// running and can be deleted.
func clearOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.Remove(exe + ".old"); err == nil {
		log.Printf("update: removed the previous build")
	}
}

// restartSelf launches the (new) binary and lets this process go. The
// child is detached, so the exiting parent does not take it with it.
func restartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// watchUpdates checks on start and then occasionally. The first check is
// delayed a little: a companion that has just launched has a page to
// render and a service to poll, and neither should queue behind GitHub.
func (a *app) watchUpdates(ctx context.Context) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.checkUpdate(ctx)
			timer.Reset(updateCheckEvery)
		}
	}
}
