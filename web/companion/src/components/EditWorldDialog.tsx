import { useState } from "react";
import { toast } from "sonner";
import { api, errorText } from "../lib/api";
import { useRefreshState } from "../lib/state";
import type { Link, SyncWorld } from "../lib/types";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

/**
 * The two things about a linked world that are not settled for good at
 * link time after all: the local folder it points at, and the name it
 * carries on the service. Each saves independently, since one lives here
 * and the other is a call to the service.
 */
export function EditWorldDialog({
  link,
  world,
  onClose,
}: {
  link: Link;
  world: SyncWorld | undefined;
  onClose: () => void;
}) {
  const refresh = useRefreshState();
  const [dir, setDir] = useState(link.dir);
  const [name, setName] = useState(world?.world.name ?? "");
  const [savingDir, setSavingDir] = useState(false);
  const [savingName, setSavingName] = useState(false);
  const held = !!link.sessionId;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogTitle>Edit world</DialogTitle>

        <div className="mt-4">
          <Label htmlFor="edit-world-name">World name</Label>
          <Input
            id="edit-world-name"
            className="mt-1"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Button
            className="mt-2"
            disabled={savingName || !name.trim() || name.trim() === (world?.world.name ?? "")}
            onClick={async () => {
              setSavingName(true);
              try {
                await api.updateLink(link.worldId, { worldName: name.trim() });
                toast.success("world renamed");
              } catch (err) {
                toast.error(errorText(err));
              } finally {
                setSavingName(false);
                refresh();
              }
            }}
          >
            Save name
          </Button>
        </div>

        <div className="mt-5 border-t border-edge pt-4">
          <Label htmlFor="edit-world-dir">Local folder</Label>
          <Input
            id="edit-world-dir"
            className="mt-1 font-mono text-[12px]"
            value={dir}
            onChange={(e) => setDir(e.target.value)}
          />
          {held ? (
            <p className="mt-1.5 text-[12px] italic text-mist">
              Check this world in before pointing it at a different folder.
            </p>
          ) : null}
          <Button
            className="mt-2"
            disabled={savingDir || held || !dir.trim() || dir.trim() === link.dir}
            onClick={async () => {
              setSavingDir(true);
              try {
                await api.updateLink(link.worldId, { dir: dir.trim() });
                toast.success("folder updated");
              } catch (err) {
                toast.error(errorText(err));
              } finally {
                setSavingDir(false);
                refresh();
              }
            }}
          >
            Save folder
          </Button>
        </div>

        <div className="mt-5 flex justify-end">
          <Button onClick={onClose}>Close</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
