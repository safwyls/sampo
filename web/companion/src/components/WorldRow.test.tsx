import { describe, expect, it, vi, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { api } from "../lib/api";
import { WorldRow } from "./WorldRow";
import { makeLink, makeSyncWorld, renderWithProviders } from "../test/utils";

const toast = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
  info: vi.fn(),
  loading: vi.fn(),
}));
vi.mock("sonner", () => ({ toast }));

const holder = (o: Record<string, unknown> = {}) => ({
  sessionId: 7,
  username: "mira",
  expiresAt: "2026-08-22T23:00:00Z",
  claimable: false,
  ...o,
});

const show = (world = makeSyncWorld(), link = makeLink(), launchOnCheckout = true) =>
  renderWithProviders(
    <WorldRow
      link={link}
      world={world}
      me="safwyl"
      art={{}}
      configured
      launchOnCheckout={launchOnCheckout}
    />,
  );

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("WorldRow", () => {
  it("shows the folder on this machine, in full", () => {
    show();
    expect(
      screen.getByText("C:\\Users\\you\\AppData\\Roaming\\Enshrouded\\savegame"),
    ).toBeInTheDocument();
  });

  it("offers checking out a free world, and nothing else custodial", () => {
    show();
    expect(screen.getAllByRole("button", { name: /^Check out/ }).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Check in" })).not.toBeInTheDocument();
  });

  it("offers a plain checkout — no launch — alongside checkout & play", async () => {
    const checkout = vi.spyOn(api, "checkout").mockResolvedValue({});
    show();
    await userEvent.click(screen.getByRole("button", { name: "Check out" }));
    await waitFor(() => expect(checkout).toHaveBeenCalledWith(1, false, false));
  });

  it("offers check in, checkpoint and renew to the holder on this machine", () => {
    show(makeSyncWorld({ holder: holder({ username: "safwyl" }) }), makeLink({ sessionId: 7 }));
    expect(screen.getByRole("button", { name: "Check in" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Checkpoint now" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Renew hold" })).toBeInTheDocument();
  });

  // A checkpoint the service will not keep is a button that lies.
  it("hides the checkpoint verb for a world that does not keep checkpoints", () => {
    const world = makeSyncWorld({ holder: holder({ username: "safwyl" }) });
    world.world.checkpoints = false;
    show(world, makeLink({ sessionId: 7 }));
    expect(screen.queryByRole("button", { name: "Checkpoint now" })).not.toBeInTheDocument();
  });

  // The account holds it, but the save is still on its way to this
  // machine: checking in here would upload a folder that has not received
  // it yet.
  it("offers nothing custodial while the world is still being fetched here", () => {
    show(makeSyncWorld({ holder: holder({ username: "safwyl", sessionId: 9 }) }), makeLink({ sessionId: 7 }));
    expect(screen.getByText(/fetching it to this machine/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Check in" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Check out/ })).not.toBeInTheDocument();
  });

  it("offers a takeover only once the hold has expired, and says what survives", async () => {
    const checkout = vi.spyOn(api, "checkout").mockResolvedValue({});
    show(makeSyncWorld({ holder: holder({ claimable: true }) }));
    await userEvent.click(screen.getByRole("button", { name: "Take over expired hold" }));
    expect(
      await screen.findByText("The old holder's late check-in is kept and flagged, not lost."),
    ).toBeInTheDocument();
    expect(checkout).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Take over" }));
    await waitFor(() => expect(checkout).toHaveBeenCalledWith(1, true, true));
  });

  it("offers to claim next only when nobody has", () => {
    const { unmount } = show(makeSyncWorld({ holder: holder() }));
    expect(screen.getByRole("button", { name: "Claim next" })).toBeInTheDocument();
    unmount();
    show(makeSyncWorld({ holder: holder(), claimedBy: "torv" }));
    expect(screen.queryByRole("button", { name: "Claim next" })).not.toBeInTheDocument();
    expect(screen.getByText(/next claim: torv/)).toBeInTheDocument();
  });

  it("says you're next rather than naming you", () => {
    show(makeSyncWorld({ holder: holder(), claimedBy: "safwyl" }));
    expect(screen.getByText(/you're next/)).toBeInTheDocument();
  });

  // A link to a world the service no longer has must say so, not render a
  // row of verbs that all fail.
  it("reports a world that has left the service", () => {
    renderWithProviders(
      <WorldRow link={makeLink()} world={undefined} me="safwyl" art={{}} configured launchOnCheckout />,
    );
    expect(screen.getByText(/is not on the service any more/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Check out/ })).not.toBeInTheDocument();
  });

  it("asks before unlinking, and promises nothing is deleted", async () => {
    const unlink = vi.spyOn(api, "unlink").mockResolvedValue({});
    show();
    await userEvent.click(screen.getByRole("button", { name: "Unlink" }));
    expect(await screen.findByText("Nothing is deleted.")).toBeInTheDocument();
    expect(unlink).not.toHaveBeenCalled();
    const confirms = screen.getAllByRole("button", { name: "Unlink" });
    await userEvent.click(confirms[confirms.length - 1]);
    await waitFor(() => expect(unlink).toHaveBeenCalledWith(1));
  });
});

// Checking a world out is two halves of one intention: fetch the save,
// then play. The button promises only what will actually happen.
describe("WorldRow — checking out and playing", () => {
  it("promises to play when the setting is on and the game can be started", () => {
    show();
    expect(screen.getByRole("button", { name: "Check out & play" })).toBeInTheDocument();
  });

  it("promises only the save when the setting is off", () => {
    show(makeSyncWorld(), makeLink(), false);
    expect(screen.getByRole("button", { name: "Check out & host" })).toBeInTheDocument();
  });

  // A folder linked by hand carries no app id and nothing that says what
  // starts it. Promising to play would be a lie.
  it("promises only the save for a world with nothing to start", () => {
    show(makeSyncWorld(), makeLink({ appId: "" }));
    expect(screen.getByRole("button", { name: "Check out & host" })).toBeInTheDocument();
  });

  it("says both halves happened when both did", async () => {
    vi.spyOn(api, "checkout").mockResolvedValue({ launched: true });
    show();
    await userEvent.click(screen.getByRole("button", { name: "Check out & play" }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        "checked out — the save is on this machine, and the game is starting",
      ),
    );
  });

  // The custody half succeeded; saying "failed" would send the player
  // looking for a save that is already exactly where it should be.
  it("reports a game that would not start without calling the checkout a failure", async () => {
    vi.spyOn(api, "checkout").mockResolvedValue({ launched: false, launchError: "steam is not installed" });
    show();
    await userEvent.click(screen.getByRole("button", { name: "Check out & play" }));
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        "checked out, but the game did not start: steam is not installed",
      ),
    );
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("takes over an expired hold through the same two-half path", async () => {
    const checkout = vi.spyOn(api, "checkout").mockResolvedValue({ launched: true });
    show(makeSyncWorld({ holder: holder({ claimable: true }) }));
    await userEvent.click(screen.getByRole("button", { name: "Take over expired hold" }));
    await userEvent.click(await screen.findByRole("button", { name: "Take over" }));
    await waitFor(() => expect(checkout).toHaveBeenCalledWith(1, true, true));
  });

  // Coming back to a world already held here, later in the same hold.
  it("offers Play to the holder, and only when there is something to start", () => {
    const { unmount } = show(
      makeSyncWorld({ holder: holder({ username: "safwyl" }) }),
      makeLink({ sessionId: 7 }),
    );
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
    unmount();
    show(makeSyncWorld({ holder: holder({ username: "safwyl" }) }), makeLink({ sessionId: 7, appId: "" }));
    expect(screen.queryByRole("button", { name: "Play" })).not.toBeInTheDocument();
  });

  it("does not offer Play for a world someone else holds", () => {
    show(makeSyncWorld({ holder: holder() }));
    expect(screen.queryByRole("button", { name: "Play" })).not.toBeInTheDocument();
  });

  it("starts the game on Play", async () => {
    const launch = vi.spyOn(api, "launch").mockResolvedValue({});
    show(makeSyncWorld({ holder: holder({ username: "safwyl" }) }), makeLink({ sessionId: 7 }));
    await userEvent.click(screen.getByRole("button", { name: "Play" }));
    await waitFor(() => expect(launch).toHaveBeenCalledWith(1));
  });
});
