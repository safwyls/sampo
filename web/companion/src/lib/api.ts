import type { Browse, CompanionState, Artwork, SplitInfo, UpdateState } from "./types";

/**
 * The companion's API answers `{ok: false, error}` rather than HTTP
 * statuses — it is a local process talking to its own page, and the page
 * shows the sentence. `call` turns that into a thrown Error so every
 * caller handles failure the same way.
 */
export async function call<T = Record<string, unknown>>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const out = (await res.json().catch(() => ({}))) as { ok?: boolean; error?: string };
  if (out.ok === false && out.error) throw new Error(out.error);
  if (!res.ok && !out.error) throw new Error(res.statusText || "the companion did not answer");
  return out as T;
}

export function errorText(err: unknown): string {
  return err instanceof Error && err.message ? err.message : "something went wrong";
}

export interface ConfigInput {
  serverUrl?: string;
  token?: string;
  steamDirs?: string[];
  launchOnCheckout?: boolean;
}

export interface LinkInput {
  worldId: number;
  gameTitle: string;
  dir: string;
  meta: string;
  appId: string;
}

export interface CreateWorldInput {
  name: string;
  gameTitle: string;
  dir: string;
  meta: string;
  appId: string;
  savePath: string;
  seed: boolean;
}

export const api = {
  state: () => call<CompanionState>("GET", "/api/state"),
  /** Only the fields present are written — the connect card and the Steam
   * card save independently, and absent means "keep what is stored". */
  setConfig: (input: ConfigInput) => call("PUT", "/api/config", input),
  discover: () => call<{ found: number }>("POST", "/api/discover"),
  syncNow: () => call<{ worlds: number }>("POST", "/api/sync/refresh"),
  artwork: () =>
    call<{ art?: Record<string, Artwork>; asked?: boolean; error?: string }>("GET", "/api/artwork"),
  saveHints: () =>
    call<{ available?: boolean; known?: number; error?: string }>("GET", "/api/savehints"),
  browse: (path: string) =>
    call<{ browse: Browse }>("GET", `/api/browse?path=${encodeURIComponent(path || "")}`),
  splitSavePath: (dir: string, appId: string, name: string) =>
    call<{ split: SplitInfo | null }>(
      "GET",
      `/api/savepath/split?${new URLSearchParams({ dir, appId, name }).toString()}`,
    ),
  resolveSavePath: (root: string, leaf: string, create: boolean) =>
    call<{ dir: string; exists: boolean }>("POST", "/api/savepath/resolve", { root, leaf, create }),
  hide: (key: string, hidden: boolean) => call("POST", "/api/hide", { key, hidden }),

  /** Ask GitHub now rather than waiting for the six-hourly check. */
  checkUpdate: () => call<{ update?: UpdateState }>("POST", "/api/update/check"),
  /** Replace this build and restart into the new one. The companion
   * answers before it restarts, so a success here means the swap
   * happened and the process is about to go. */
  applyUpdate: () => call<{ restarting?: boolean }>("POST", "/api/update/apply"),

  addLink: (input: LinkInput) => call("POST", "/api/links", input),
  createWorld: (input: CreateWorldInput) => call("POST", "/api/links/create", input),
  unlink: (worldID: number) => call("DELETE", `/api/links/${worldID}`),
  /** Takes the world, installs its save, and — when the setting allows
   * and the world has something to start — plays it. The answer says
   * which of those happened: a save on disk with a game that would not
   * start is a real outcome, not a failure. */
  checkout: (worldID: number, takeover: boolean, play = true) =>
    call<{ launched?: boolean; launchError?: string }>(
      "POST",
      `/api/links/${worldID}/checkout`,
      { takeover, play },
    ),
  /** Start the game for a world already held here. */
  launch: (worldID: number) => call("POST", `/api/links/${worldID}/launch`),
  updateLink: (worldID: number, input: { launchTarget?: string; dir?: string; worldName?: string }) =>
    call("PUT", `/api/links/${worldID}`, input),
  checkin: (worldID: number) => call("POST", `/api/links/${worldID}/checkin`),
  checkpoint: (worldID: number) => call("POST", `/api/links/${worldID}/checkpoint`),
  renew: (worldID: number) => call("POST", `/api/links/${worldID}/renew`),
  claim: (worldID: number) => call("POST", `/api/links/${worldID}/claim`),
};
