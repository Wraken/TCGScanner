import { useEffect, useRef, useState } from "react";
import { GetCardImage, GetCardInfo } from "../../wailsjs/go/main/App";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CardImage } from "@/components/CardImage";
import type { Candidate, CardInfo } from "@/types";

interface Props {
  candidates: Candidate[] | null; // null = closed
  preselected: string | null;
  onPick: (cardId: string) => void;
  onDismiss: () => void;
}

// Asks which printing a scanned card is when several share the artwork.
// The preselected tile takes focus on open, so Enter picks it; Escape, the
// close button and "None of these" dismiss and let scanning resume.
export function VariantChoiceDialog({ candidates, preselected, onPick, onDismiss }: Props) {
  // Artwork and metadata per card id, kept for the dialog's lifetime: a
  // batch reopens on the same printings.
  const [images, setImages] = useState<Record<string, string>>({});
  const [infos, setInfos] = useState<Record<string, CardInfo | null>>({});
  const preselectedRef = useRef<HTMLButtonElement | null>(null);
  const open = candidates !== null;

  useEffect(() => {
    if (!candidates) return;
    let cancelled = false;
    for (const c of candidates) {
      if (!(c.card_id in images)) {
        GetCardImage(c.card_id)
          .then((img) => { if (!cancelled) setImages((prev) => ({ ...prev, [c.card_id]: img })); })
          .catch(() => {});
      }
      if (!Object.hasOwn(infos, c.card_id)) {
        GetCardInfo(c.card_id)
          .then((info) => { if (!cancelled) setInfos((prev) => ({ ...prev, [c.card_id]: info as CardInfo })); })
          .catch(() => { if (!cancelled) setInfos((prev) => ({ ...prev, [c.card_id]: null })); });
      }
    }
    return () => { cancelled = true; };
  }, [candidates]);

  const columns = Math.min(candidates?.length ?? 1, 3);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onDismiss()}>
      <DialogContent
        className="max-w-4xl max-h-[85vh] overflow-y-auto"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          preselectedRef.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>Which printing is it?</DialogTitle>
          <p className="text-xs n-n500">
            These cards share the same artwork — check the number at the bottom of your card.
          </p>
        </DialogHeader>
        <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
          {(candidates ?? []).map((c) => {
            const info = infos[c.card_id] ?? null;
            const selected = c.card_id === preselected;
            return (
              <button
                key={c.card_id}
                type="button"
                ref={selected ? preselectedRef : undefined}
                className="flex flex-col gap-2 rounded-lg p-2 text-left"
                style={{ border: `1px solid ${selected ? "var(--n-accent)" : "var(--n-divider)"}` }}
                onClick={() => onPick(c.card_id)}
              >
                <CardImage src={images[c.card_id] ?? null} alt={c.card_id} adaptive className="w-full rounded-md" />
                <span className="font-mono text-xs n-n400">{c.card_id}</span>
                {info && (
                  <div className="flex flex-wrap gap-1.5">
                    {(info.set_name || info.set) && (
                      <span className="n-tag n-tag-neutral">
                        {info.set_name || info.set}
                        {info.set_name && info.set && <span className="font-mono n-n500">{info.set}</span>}
                      </span>
                    )}
                    {info.number && <span className="n-tag n-tag-neutral font-mono">#{info.number}</span>}
                    {info.rarity && <span className="n-tag n-tag-neutral">{info.rarity}</span>}
                  </div>
                )}
              </button>
            );
          })}
        </div>
        <DialogFooter>
          <button type="button" className="n-btn n-btn-secondary" onClick={onDismiss}>
            None of these
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
