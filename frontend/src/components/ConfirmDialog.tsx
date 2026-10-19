import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface Props {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  onConfirm: () => Promise<void> | void;
  onOpenChange: (open: boolean) => void;
}

// Confirms a destructive action. The button names the action, never "OK".
// Focus starts on Cancel so Enter alone never destroys anything.
export function ConfirmDialog({ open, title, description, confirmLabel, cancelLabel = "Cancel", onConfirm, onOpenChange }: Props) {
  const [busy, setBusy] = useState(false);

  async function confirm() {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="text-sm n-n400">{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <button className="n-btn n-btn-secondary" onClick={() => onOpenChange(false)}>
            {cancelLabel}
          </button>
          <button className="n-btn n-btn-danger" onClick={confirm} disabled={busy}>
            {confirmLabel}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
