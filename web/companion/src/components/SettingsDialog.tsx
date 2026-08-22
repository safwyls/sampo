import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { api, errorText } from "../lib/api";
import { useRefreshState, useSeededField } from "../lib/state";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import type { CompanionState } from "../lib/types";

/**
 * The two settings that are not about a world: where the vault is, and
 * where Steam is. Both save independently — the companion's config
 * endpoint writes only the fields a request carries — so a saved token
 * survives a Steam-folder change and vice versa.
 */
export function SettingsDialog({
  state,
  onClose,
}: {
  state: CompanionState;
  onClose: () => void;
}) {
  const refresh = useRefreshState();
  const url = useSeededField(state.config?.serverUrl ?? "");
  const steam = useSeededField(state.config?.steamDirs?.[0] ?? "");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const launchOnCheckout = state.config?.launchOnCheckout ?? true;
  const update = state.update;
  const version = state.version;

  const checkUpdate = async () => {
    setCheckingUpdate(true);
    try {
      const { update: u } = await api.checkUpdate();
      if (u?.error) {
        toast.error(u.error);
      } else if (u?.available) {
        toast.success(`update available: ${u.version}`);
      } else {
        toast.success("you're up to date");
      }
    } catch (err) {
      toast.error(errorText(err));
    } finally {
      setCheckingUpdate(false);
      refresh();
    }
  };

  const connect = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      // A completed connection is proven with a status poll on the
      // companion's side: a typo'd token fails here, not silently every
      // minute forever. An empty token keeps the saved one.
      await api.setConfig({ serverUrl: url.value, token });
      setToken("");
      url.settle();
      toast.success("connected");
    } catch (err) {
      toast.error(errorText(err));
    } finally {
      setBusy(false);
      refresh();
    }
  };

  const saveSteam = async () => {
    const dir = steam.value.trim();
    setBusy(true);
    try {
      await api.setConfig({ steamDirs: dir ? [dir] : [] });
      steam.settle(dir);
      toast.success(dir ? "folder saved — rescanned" : "override cleared — rescanned");
    } catch (err) {
      toast.error(errorText(err));
    } finally {
      setBusy(false);
      refresh();
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-[520px]">
        <DialogTitle>Settings</DialogTitle>
        <form onSubmit={connect} className="mt-4 flex flex-col gap-2">
          <Label htmlFor="settings-url">Save-sync service URL</Label>
          <Input id="settings-url" placeholder="https://vault.example.com" {...url.props} />
          <Label htmlFor="settings-token" className="mt-1">
            Your sync token {state.config?.tokenSet ? "(saved — paste to replace)" : ""}
          </Label>
          <Input
            id="settings-token"
            type="password"
            placeholder="paste the token from the service's page"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <div className="mt-2">
            <Button type="submit" variant="primary" disabled={busy}>
              Save &amp; connect
            </Button>
          </div>
        </form>

        <div className="mt-6 border-t border-edge pt-4">
          <Label htmlFor="settings-steam">Steam folder (blank = auto-detect)</Label>
          <Input
            id="settings-steam"
            className="mt-1 font-mono text-[12px]"
            placeholder="e.g. D:\SteamLibrary or D:\Steam\steamapps\common"
            {...steam.props}
          />
          <p className="mt-1.5 text-[12px] italic text-mist">
            Paste the Steam root, steamapps, or steamapps\common — extra libraries on other drives are found from it.
          </p>
          <Button type="button" className="mt-2" disabled={busy} onClick={saveSteam}>
            Save folder &amp; rescan
          </Button>
        </div>

        <div className="mt-6 border-t border-edge pt-4">
          <label className="flex items-start gap-2.5 text-[14px]">
            <input
              type="checkbox"
              className="mt-1"
              checked={launchOnCheckout}
              onChange={async (e) => {
                try {
                  await api.setConfig({ launchOnCheckout: e.target.checked });
                } catch (err) {
                  toast.error(errorText(err));
                } finally {
                  refresh();
                }
              }}
            />
            <span>
              Start the game when I check a world out
              <span className="mt-0.5 block text-[12px] italic text-mist">
                The save is put in place first, then the game starts — never the other way round. Switch it off to
                take custody of a world without opening it. Games linked by hand carry nothing that says what starts
                them, so those check out without launching either way.
              </span>
            </span>
          </label>
        </div>

        <div className="mt-6 border-t border-edge pt-4">
          <Label>Companion version {version ? <span className="text-mist">({version})</span> : null}</Label>
          <p className="mt-1 text-[12px] italic text-mist">
            {update?.error
              ? `last check failed: ${update.error}`
              : update?.available
                ? `an update is available: ${update.version}`
                : update?.checkedAt
                  ? "up to date, as of the last check"
                  : "checked automatically every few hours"}
          </p>
          <Button type="button" className="mt-2" disabled={checkingUpdate} onClick={checkUpdate}>
            Check for update
          </Button>
        </div>

        <div className="mt-5 flex justify-end">
          <Button type="button" onClick={onClose}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
