import { useState } from "react";
import { toast } from "sonner";
import { api, errorText } from "./lib/api";
import { useArtwork, useCompanionState, useRefreshState, useSaveHints } from "./lib/state";
import { FirstRun } from "./components/FirstRun";
import { FooterBar, HeaderBar } from "./components/HeaderBar";
import { LinkGameDialog, byHandGame } from "./components/LinkGameDialog";
import { LinkedGameDialog } from "./components/LinkedGameDialog";
import { PanelBoundary, SectionHeader } from "./components/Panel";
import { SettingsDialog } from "./components/SettingsDialog";
import { UpdateBanner } from "./components/UpdateBanner";
import { Shelf, linkFor } from "./components/Shelf";
import { WorldRow } from "./components/WorldRow";
import { tileKey } from "./components/GameTile";
import type { DiscoveredGame } from "./lib/types";

export function App() {
  const refresh = useRefreshState();
  const { data: state, isLoading, isError, error } = useCompanionState();
  const games = state?.discovered?.games ?? [];
  // Both of these ask when the *set* of games changes, never on the poll.
  const { art, empty: artEmpty, error: artError } = useArtwork(games);
  const hints = useSaveHints(games, Boolean(state?.sync?.configured));

  /** Which shelf entry is open, if any. Held here rather than in the
   * shelf so a poll that rebuilds tiles cannot close it. */
  const [open, setOpen] = useState<DiscoveredGame | null>(null);
  const [settings, setSettings] = useState(false);

  if (isLoading) {
    return <p className="p-8 text-mist">Reading this machine…</p>;
  }
  if (isError || !state) {
    return (
      <p className="p-8 font-mono text-[13px] text-ember">
        The companion is not answering on this machine: {errorText(error)}
      </p>
    );
  }

  const links = state.links ?? [];
  const worlds = state.sync?.worlds ?? [];
  const openLink = open ? linkFor(open, links) : undefined;

  const rescan = async () => {
    try {
      const out = await api.discover();
      toast.success(`rescanned — ${out.found} game${out.found === 1 ? "" : "s"} found`);
    } catch (err) {
      toast.error(errorText(err));
    }
    refresh();
  };

  return (
    <div className="flex min-h-screen flex-col">
      <HeaderBar state={state} onOpenSettings={() => setSettings(true)} />

      {state.sync?.configured ? (
        <main className="flex flex-1 flex-col gap-5 px-7 py-6">
          <PanelBoundary name="update">
            <UpdateBanner update={state.update} />
          </PanelBoundary>
          <PanelBoundary name="worlds">
            <section className="flex flex-col gap-2.5">
              <SectionHeader title="Your worlds" />
              {links.length ? (
                links.map((link) => (
                  <WorldRow
                    key={link.worldId}
                    link={link}
                    world={worlds.find((w) => w.world.id === link.worldId)}
                    me={state.sync?.username}
                    art={art}
                    configured
                    launchOnCheckout={state.config?.launchOnCheckout ?? true}
                  />
                ))
              ) : (
                <p className="text-[13px] italic text-mist">
                  Nothing linked yet — link an installed game below, or ask whoever runs your sync service which
                  world to join.
                </p>
              )}
            </section>
          </PanelBoundary>

          <PanelBoundary name="shelf">
            <Shelf
              state={state}
              art={art}
              artEmpty={artEmpty}
              artError={artError}
              hints={hints}
              activeKey={open ? tileKey(open) : null}
              onOpen={setOpen}
              onRescan={rescan}
              onLinkByHand={() => setOpen(byHandGame())}
            />
          </PanelBoundary>
        </main>
      ) : (
        <PanelBoundary name="setup">
          <div className="flex flex-1 flex-col">
            {state.update?.available ? (
              <div className="px-7 pt-6">
                <UpdateBanner update={state.update} />
              </div>
            ) : null}
            <FirstRun state={state} />
          </div>
        </PanelBoundary>
      )}

      <FooterBar state={state} />

      {/* A linked entry opens what it points at; an unlinked one opens the
          link form. Both are dialogs, so the poll under them is free to
          rebuild the shelf. */}
      {open && openLink ? (
        <LinkedGameDialog
          game={open}
          link={openLink}
          world={worlds.find((w) => w.world.id === openLink.worldId)}
          art={art}
          onClose={() => setOpen(null)}
        />
      ) : null}
      {open && !openLink ? (
        <LinkGameDialog game={open} state={state} art={art} onClose={() => setOpen(null)} />
      ) : null}
      {settings ? <SettingsDialog state={state} onClose={() => setSettings(false)} /> : null}
    </div>
  );
}
