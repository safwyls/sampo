import { describe, expect, it, vi, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { api } from "../lib/api";
import { UpdateBanner } from "./UpdateBanner";
import { renderWithProviders } from "../test/utils";
import type { UpdateState } from "../lib/types";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn(), loading: vi.fn() },
}));

const update = (o: Partial<UpdateState> = {}): UpdateState => ({
  available: true,
  version: "abcdef123456",
  supported: true,
  ...o,
});

afterEach(() => vi.restoreAllMocks());

describe("UpdateBanner", () => {
  it("stays out of the way when there is nothing to offer", () => {
    const { container } = renderWithProviders(<UpdateBanner update={update({ available: false })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says nothing at all before the first check has answered", () => {
    const { container } = renderWithProviders(<UpdateBanner update={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  // Every build is stamped with a commit SHA and SHAs have no order, so
  // the honest claim is "different", not "newer".
  it("names the build without claiming it is newer", () => {
    renderWithProviders(<UpdateBanner update={update()} />);
    expect(screen.getByText("A different companion build is available.")).toBeInTheDocument();
    expect(screen.getByText("abcdef123456")).toBeInTheDocument();
    expect(screen.queryByText(/newer/i)).not.toBeInTheDocument();
  });

  // An install that cannot write to its own folder (Program Files
  // without elevation) would fail on the button, so it doesn't get one.
  it("explains rather than offering a button that cannot work", () => {
    renderWithProviders(
      <UpdateBanner
        update={update({ supported: false, why: "this companion lives somewhere it cannot write to" })}
      />,
    );
    expect(screen.getByText(/cannot write to/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Update now" })).not.toBeInTheDocument();
  });

  it("applies on request and says the companion is restarting", async () => {
    const apply = vi.spyOn(api, "applyUpdate").mockResolvedValue({ restarting: true });
    renderWithProviders(<UpdateBanner update={update()} />);
    await userEvent.click(screen.getByRole("button", { name: "Update now" }));
    await waitFor(() => expect(apply).toHaveBeenCalled());
    expect(await screen.findByRole("button", { name: "Updating…" })).toBeInTheDocument();
  });

  // A refused update must leave the button usable — the reason is often
  // "a save transfer is running", which stops being true a minute later.
  it("comes back from a refusal", async () => {
    vi.spyOn(api, "applyUpdate").mockRejectedValue(new Error("a save transfer is running"));
    renderWithProviders(<UpdateBanner update={update()} />);
    await userEvent.click(screen.getByRole("button", { name: "Update now" }));
    expect(await screen.findByRole("button", { name: "Update now" })).toBeEnabled();
  });

  it("shows an apply already running as in progress", () => {
    renderWithProviders(<UpdateBanner update={update({ applying: true })} />);
    expect(screen.getByRole("button", { name: "Updating…" })).toBeDisabled();
  });
});
