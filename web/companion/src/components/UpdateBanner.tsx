import { useState } from "react";
import { ArrowUpCircle } from "lucide-react";
import { toast } from "sonner";
import { api, errorText } from "../lib/api";
import { useRefreshState } from "../lib/state";
import { Button } from "./ui/button";
import type { UpdateState } from "../lib/types";

/**
 * A new build, offered rather than imposed. The companion checks GitHub
 * on its own, but replacing a binary underneath someone is not something
 * to do silently — so this says what is available and waits to be asked.
 *
 * It says "a different build" rather than "a newer version" on purpose:
 * every release is stamped with a commit SHA, SHAs have no order, and
 * the honest question is whether the release ships the build you are
 * running, not whether its number is bigger.
 */
export function UpdateBanner({ update }: { update: UpdateState | undefined }) {
  const refresh = useRefreshState();
  const [applying, setApplying] = useState(false);
  if (!update?.available) return null;

  const apply = async () => {
    setApplying(true);
    try {
      await api.applyUpdate();
      // The companion answers and *then* restarts, so this is the last
      // thing this page hears from that process. Reloading walks into a
      // closed port; wait for the replacement to bind it.
      toast.success("updated — the companion is restarting");
      setTimeout(() => window.location.reload(), 2500);
    } catch (err) {
      toast.error(errorText(err));
      setApplying(false);
      refresh();
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-panel border border-gold/50 bg-[#23180c] px-5 py-3">
      <ArrowUpCircle className="h-5 w-5 flex-none text-gold" strokeWidth={1.4} aria-hidden />
      <div className="flex-1 text-[13px]">
        <span className="text-[14px] text-parchment">A different companion build is available.</span>{" "}
        <span className="font-mono text-mist">{update.version}</span>
        {update.supported ? (
          <span className="text-mist"> — it replaces this one and restarts.</span>
        ) : (
          // Offering a button that cannot work is worse than saying why.
          <span className="text-mist"> {update.why}</span>
        )}
      </div>
      {update.supported ? (
        <Button variant="primary" onClick={apply} disabled={applying || update.applying}>
          {applying || update.applying ? "Updating…" : "Update now"}
        </Button>
      ) : null}
    </div>
  );
}
