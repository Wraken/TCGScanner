import { useEffect, useRef, useState } from "react";
import { GetCardImage, GetCardInfo, GetCardPrices, GetPriceRefs } from "../../wailsjs/go/main/App";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CardImage } from "@/components/CardImage";
import { copyValue, formatEUR } from "@/lib/prices";
import type { Candidate, CardInfo, CardPrice, PriceRef } from "@/types";

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
  const [refs, setRefs] = useState<Record<string, PriceRef>>({});
  const [prices, setPrices] = useState<Record<string, CardPrice>>({});
  const preselectedRef = useRef<HTMLButtonElement | null>(null);
  const open = candidates !== null;

  useEffect(() => {
    if (!candidates) return;
    let cancelled = false;
    for (const c of candidates) {
      if (!Object.hasOwn(images, c.card_id)) {
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

  useEffect(() => {
    // Drop the previous candidates' data first, so a changed reference never
    // flashes its old label.
    setRefs({});
    setPrices({});
    if (!candidates) return;
    let cancelled = false;
    const ids = candidates.map((c) => c.card_id);
    Promise.all([GetPriceRefs().catch(() => ({})), GetCardPrices(ids).catch(() => ({}))]).then(([r, p]) => {
      if (cancelled) return;
      setRefs((r ?? {}) as Record<string, PriceRef>);
      setPrices((p ?? {}) as Record<string, CardPrice>);
    });
    return () => { cancelled = true; };
  }, [candidates]);

  const columns = Math.min(candidates?.length ?? 1, 3);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onDismiss()}>
      <DialogContent
        className="max-w-4xl max-h-[85vh] flex flex-col"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          preselectedRef.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>Which printing is it?</DialogTitle>
          <DialogDescription>
            These printings share the same artwork. Check the number printed at the bottom of your card, and the Cardmarket printing you chose for each.
          </DialogDescription>
        </DialogHeader>
        <div
          className="grid gap-4 justify-center overflow-y-auto min-h-0 p-1"
          style={{ gridTemplateColumns: `repeat(${columns}, 200px)` }}
        >
          {(candidates ?? []).map((c) => {
            const info = infos[c.card_id] ?? null;
            const selected = c.card_id === preselected;
            return (
              <button
                key={c.card_id}
                type="button"
                ref={selected ? preselectedRef : undefined}
                className="flex flex-col gap-2 rounded-lg p-2 text-left transition-colors hover:border-[var(--n-neutral-700)]"
                style={{ border: `1px solid ${selected ? "var(--n-accent)" : "var(--n-divider)"}` }}
                onClick={() => onPick(c.card_id)}
              >
                <CardImage src={images[c.card_id] ?? null} alt="" adaptive className="w-full rounded-md" />
                <span className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs n-n400" translate="no">{c.card_id}</span>
                  {selected && <span className="n-tag n-tag-outline" style={{ fontSize: "10px", padding: "1px 7px" }}>Suggested</span>}
                </span>
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
                {(() => {
                  const ref = refs[c.card_id];
                  const price = prices[c.card_id];
                  if (ref) {
                    const v = copyValue({ foil: false, lang: "en" }, price);
                    return (
                      <span className="text-xs n-n400">
                        {ref.label}{v !== null ? ` · ${formatEUR(v)}` : ""}
                      </span>
                    );
                  }
                  if (price && price.candidates > 0) {
                    return <span className="text-xs" style={{ color: "var(--n-warn)" }}>Price reference not set</span>;
                  }
                  return null;
                })()}
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
