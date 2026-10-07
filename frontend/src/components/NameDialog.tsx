import { useEffect, useId, useState } from "react";
import type { FormEvent } from "react";
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
  description?: string;
  submitLabel: string;
  initialValue?: string;
  onSubmit: (name: string) => Promise<void> | void;
  onOpenChange: (open: boolean) => void;
}

// Asks for a collection name (create, rename). Replaces window.prompt so the
// question looks like the app and Enter / Escape behave as expected.
export function NameDialog({ open, title, description, submitLabel, initialValue = "", onSubmit, onOpenChange }: Props) {
  const [name, setName] = useState(initialValue);
  const [busy, setBusy] = useState(false);
  const id = useId();

  useEffect(() => {
    if (open) setName(initialValue);
  }, [open, initialValue]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    try {
      await onSubmit(trimmed);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <label htmlFor={id} className="text-xs n-n400">Name</label>
            <input
              id={id}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              autoComplete="off"
              spellCheck={false}
              placeholder="e.g. Trade binder"
              className="n-field w-full text-sm px-3 py-2"
            />
          </div>
          <DialogFooter>
            <button type="button" className="n-btn n-btn-secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </button>
            <button type="submit" className="n-btn n-btn-primary" disabled={!name.trim() || busy}>
              {submitLabel}
            </button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
