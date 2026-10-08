import { useEffect, useState } from "react";
import { ClearPriceRef, GetPriceCandidateImage, GetPriceCandidates, SetPriceRef } from "../../wailsjs/go/main/App";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CardImage } from "@/components/CardImage";
import { errorText, useNotices } from "@/components/Notices";
import { formatEUR } from "@/lib/prices";
import type { CardInfo, PriceCandidateView } from "@/types";

interface Props {
  cardId: string | null; // null = closed
  image: string | null;  // our reference image of the card
  info: CardInfo | null;
  onClose: () => void;
}

// "Trend €3.20 · foil €3.50 · other languages €2.00", the columns a candidate has.
function priceText(p: PriceCandidateView["price"]): string {
  const parts: string[] = [];
  if (p.trend > 0) parts.push(`Trend ${formatEUR(p.trend)}`);
  if (p.trend_foil > 0) parts.push(`foil ${formatEUR(p.trend_foil)}`);
  if (p.trend_non_english > 0) parts.push(`other languages ${formatEUR(p.trend_non_english)}`);
  return parts.length ? parts.join(" · ") : "No price today";
}

// Asks which Cardmarket printing a card id is, among the candidates the
// bundle ships, with CardTrader's scan and today's price per printing.
// The choice is stored per card id; digits pick a tile, Escape closes.
export function PriceReferenceDialog({ cardId, image, info, onClose }: Props) {
  const { notify } = useNotices();
  const [views, setViews] = useState<PriceCandidateView[]>([]);
  const [scans, setScans] = useState<Record<string, string | null>>({}); // url -> data URL, null = failed
  const [busy, setBusy] = useState(false);
  const open = cardId !== null;

  useEffect(() => {
    if (!cardId) return;
    let cancelled = false;
    setViews([]);
    GetPriceCandidates(cardId)
      .then((v) => { if (!cancelled) setViews((v ?? []) as PriceCandidateView[]); })
      .catch((e) => { if (!cancelled) notify("error", `Couldn't load the printings: ${errorText(e)}`); });
    return () => { cancelled = true; };
  }, [cardId]);

  // One scan at a time, in tile order; a failure leaves a "no image" box.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      for (const v of views) {
        if (!v.image || Object.hasOwn(scans, v.image)) continue;
        const data = await GetPriceCandidateImage(v.image).catch(() => "");
        if (cancelled) return;
        setScans((prev) => ({ ...prev, [v.image]: data || null }));
      }
    })();
    return () => { cancelled = true; };
  }, [views]);

  async function choose(product: number | null) {
    if (!cardId) return;
    setBusy(true);
    try {
      if (product === null) await ClearPriceRef(cardId);
      else await SetPriceRef(cardId, product);
      onClose();
    } catch (e) {
      notify("error", `Couldn't save the reference: ${errorText(e)}`);
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      const i = parseInt(e.key, 10);
      if (!isNaN(i) && i >= 1 && i <= views.length) choose(views[i - 1].product);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, views]);

  const columns = Math.min(Math.max(views.length, 1), 3);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-5xl max-h-[88vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Which Cardmarket printing is {cardId}?</DialogTitle>
          <DialogDescription>
            {info?.name ? `${info.name}. ` : ""}Compare your card with CardTrader's scans: the foil pattern and the stamp show there. Press a tile's number to choose it.
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-6 min-h-0">
          <div className="w-[220px] shrink-0">
            <CardImage src={image} alt={cardId ?? ""} adaptive className="w-full rounded-md" />
            <p className="m-0 mt-2 text-xs n-n500">Our reference image</p>
          </div>
          <div className="grid gap-4 overflow-y-auto min-h-0 p-1 flex-1 content-start" style={{ gridTemplateColumns: `repeat(${columns}, minmax(180px, 1fr))` }}>
            {views.map((v, i) => {
              const scan = v.image ? scans[v.image] : null;
              return (
                <button
                  key={v.product}
                  type="button"
                  disabled={busy}
                  className="flex flex-col gap-2 rounded-lg p-2 text-left transition-colors hover:border-[var(--n-neutral-700)]"
                  style={{ border: `1px solid ${v.chosen ? "var(--n-accent)" : "var(--n-divider)"}` }}
                  onClick={() => choose(v.product)}
                >
                  {scan ? (
                    <img src={scan} alt="" className="w-full rounded-md" style={{ aspectRatio: "63 / 88", objectFit: "contain", background: "#000" }} />
                  ) : (
                    <div className="w-full rounded-md flex items-center justify-center n-n500 text-xs" style={{ aspectRatio: "63 / 88", background: "#000" }}>
                      {v.image && scan === undefined ? "Loading…" : "No image"}
                    </div>
                  )}
                  <span className="flex items-center gap-2">
                    <kbd className="n-tag n-tag-outline font-mono" style={{ fontSize: "10px", padding: "1px 7px" }}>{i + 1}</kbd>
                    <span className="text-sm">{v.label}</span>
                  </span>
                  <span className="text-xs n-n400 tabular-nums">{priceText(v.price)}</span>
                  {v.chosen && <span className="n-tag n-tag-outline" style={{ fontSize: "10px", padding: "1px 7px" }}>Chosen</span>}
                </button>
              );
            })}
            {views.length === 0 && <p className="m-0 text-sm n-n400">No printing to choose from.</p>}
          </div>
        </div>
        <DialogFooter>
          <button type="button" className="n-btn n-btn-secondary" disabled={busy} onClick={() => choose(null)}>
            None of these
          </button>
          <button type="button" className="n-btn n-btn-secondary" onClick={onClose}>
            Close
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
