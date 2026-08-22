import { useState } from "react";
import { toast } from "sonner";
import { api, errorText } from "../lib/api";
import { useRefreshState } from "../lib/state";
import { custodyOf, launchable, type Artwork, type Link, type SyncWorld } from "../lib/types";
import { CoverArt } from "./CoverArt";
import { CustodyChip, custodyLine } from "./CustodyChip";
import { ConfirmDialog } from "./ConfirmDialog";
import { EditWorldDialog } from "./EditWorldDialog";
import { Button } from "./ui/button";

/**
 * One linked world: what it is, who holds it, the folder on this machine,
 * and the single action its custody state calls for. These verbs and
 * their visibility are the old page's `linkedRow()`, rule for rule —
 * including that a hold belonging to this account but to a *different*
 * session offers nothing, because the download is still on its way here.
 */
export function WorldRow({
  link,
  world,
  me,
  art,
  configured,
  launchOnCheckout,
}: {
  link: Link;
  world: SyncWorld | undefined;
  me: string | undefined;
  art: Record<string, Artwork>;
  configured: boolean;
  /** The setting, so the button can promise only what will happen. */
  launchOnCheckout: boolean;
}) {
  const refresh = useRefreshState();
  const [editing, setEditing] = useState(false);
  const custody = custodyOf(link, world, me, configured);
  const title = link.gameTitle || world?.world.name || "";
  // "& play" only when both halves are true: the setting is on, and this
  // world has something to start. A world linked by hand from a folder
  // has no app id, so the button goes back to promising the save alone.
  const willPlay = launchOnCheckout && launchable(link);

  const run = async (fn: () => Promise<unknown>, okMsg?: string) => {
    try {
      await fn();
      if (okMsg) toast.success(okMsg);
    } catch (err) {
      toast.error(errorText(err));
    } finally {
      refresh();
    }
  };

  /**
   * Checking out is two halves of one intention: fetch the save, then
   * play. The companion does them in that order and reports both, because
   * a save on disk with a game that would not start is a real outcome —
   * the custody half succeeded and the player needs to know the other
   * half did not, without being told the whole thing failed.
   */
  const checkout = async (takeover: boolean, play = true) => {
    try {
      const out = await api.checkout(link.worldId, takeover, play);
      if (out.launchError) {
        toast.warning(`checked out, but the game did not start: ${out.launchError}`);
      } else if (out.launched) {
        toast.success("checked out — the save is on this machine, and the game is starting");
      } else {
        toast.success("checked out — the save is on this machine");
      }
    } catch (err) {
      toast.error(errorText(err));
    } finally {
      refresh();
    }
  };

  return (
    <div className="flex items-start gap-3.5 rounded-panel border border-edge bg-panel px-4 py-3.5">
      <CoverArt art={art} game={{ appId: link.appId, name: title }} variant="thumb" />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <span className="text-[16px] font-bold">
            {world ? world.world.name : `world #${link.worldId}`}
          </span>
          {link.gameTitle ? <span className="text-[12px] text-rune">{link.gameTitle}</span> : null}
          <CustodyChip custody={custody} className="ml-auto" />
        </div>
        <div className="text-[13px] text-mist">{custodyLine(custody, link, world, me)}</div>
        {/* The folder, in full: the one thing on this page that is about
            this machine rather than the world, and the thing a player
            checks when a save goes to the wrong place. */}
        <div className="break-all font-mono text-[11px] text-mist">{link.dir}</div>
        <div className="flex flex-wrap gap-2">
          {custody === "free" ? (
            <>
              <Button variant="primary" onClick={() => checkout(false)}>
                {willPlay ? "Check out & play" : "Check out & host"}
              </Button>
              {/* The save alone, no launch — for taking custody without
                  starting anything, regardless of the launch-on-checkout
                  setting. */}
              {willPlay ? (
                <Button onClick={() => checkout(false, false)}>Check out</Button>
              ) : null}
            </>
          ) : null}
          {custody === "mine" ? (
            <>
              <Button
                variant="primary"
                onClick={() => run(() => api.checkin(link.worldId), "checked in — the world is free")}
              >
                Check in
              </Button>
              {/* A checkpoint never moves the head; the service only keeps
                  them for worlds that asked for them. */}
              {world?.world.checkpoints ? (
                <Button onClick={() => run(() => api.checkpoint(link.worldId), "checkpoint pushed")}>
                  Checkpoint now
                </Button>
              ) : null}
              <Button onClick={() => run(() => api.renew(link.worldId), "hold renewed")}>
                Renew hold
              </Button>
              {/* The world is already here; this is for coming back to it
                  later in the same hold, without checking anything out. */}
              {launchable(link) ? (
                <Button onClick={() => run(() => api.launch(link.worldId), "starting the game")}>
                  Play
                </Button>
              ) : null}
            </>
          ) : null}
          {custody === "expired" ? (
            <ConfirmDialog
              trigger={<Button variant="primary">Take over expired hold</Button>}
              title="Take over the expired hold?"
              body="The old holder's late check-in is kept and flagged, not lost."
              confirmLabel="Take over"
              onConfirm={() => checkout(true)}
            />
          ) : null}
          {(custody === "held" || custody === "expired") && !world?.claimedBy ? (
            <Button
              onClick={() =>
                run(
                  () => api.claim(link.worldId),
                  "you're next — the world downloads automatically when it frees up",
                )
              }
            >
              Claim next
            </Button>
          ) : null}
          <Button onClick={() => setEditing(true)}>Edit</Button>
          <ConfirmDialog
            trigger={<Button>Unlink</Button>}
            title="Unlink this world from its folder?"
            body="Nothing is deleted."
            confirmLabel="Unlink"
            onConfirm={() => run(() => api.unlink(link.worldId), "unlinked")}
          />
        </div>
      </div>
      {editing ? <EditWorldDialog link={link} world={world} onClose={() => setEditing(false)} /> : null}
    </div>
  );
}
