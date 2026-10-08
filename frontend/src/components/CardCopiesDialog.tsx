import { useEffect, useRef, useState } from "react";
import { Sparkle, Trash2, TriangleAlert } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { CardImage } from "@/components/CardImage";
import { formatDate } from "@/lib/utils";
import { formatEUR, formatGuideDate, referenceState, sumValues } from "@/lib/prices";
import type { CardInfo, CardLanguage, CardPrice, Collection, CollectionCard } from "@/types";

interface Props {
  cardId: string | null;              // null = closed
  copies: CollectionCard[];           // copies of that card id
  image: string | null;
  info: CardInfo | null;
  // Cardmarket price of the card; undefined = none.
  price?: CardPrice;
  // Cardmarket prices are on and a bundle is present; gates the reference notes.
  pricesAvailable: boolean;
  customs: Collection[];
  languages: CardLanguage[];
  onClose: () => void;
  onToggleFoil: (copy: CollectionCard) => void;
  onChangeLang: (copy: CollectionCard, code: string) => void;
  onMove: (rowId: number, customId: number) => void;
  onDelete: (rowId: number) => void;
  // Opens the price reference dialog for this card id.
  onSetReference?: () => void;
}

// "Trend €8.33 · foil €35.36 · other languages €4.56": the columns the
// card has, in that order.
function priceLine(p: CardPrice): string {
  const parts: string[] = [];
  if (p.trend > 0) parts.push(`Trend ${formatEUR(p.trend)}`);
  if (p.trend_foil > 0) parts.push(`${parts.length ? "foil" : "Foil"} ${formatEUR(p.trend_foil)}`);
  if (p.trend_non_english > 0) parts.push(`other languages ${formatEUR(p.trend_non_english)}`);
  else if (p.trend_non_english_foil > 0) parts.push(`other languages foil ${formatEUR(p.trend_non_english_foil)}`);
  return parts.join(" · ");
}

export function CardCopiesDialog(props: Props) {
  const { customs, languages, onClose, onToggleFoil, onChangeLang, onMove, onDelete } = props;
  // While the dialog fades out, its card is already null upstream; keep
  // showing the last card instead of "null · 0 copies" for the duration
  // of the exit animation.
  const shown = useRef<Pick<Props, "cardId" | "copies" | "image" | "info" | "price">>(props);
  if (props.cardId !== null) shown.current = props;
  const { cardId, copies, image, info, price } = shown.current;
  const open = props.cardId !== null;

  // The copy whose delete is armed: the trash button turns into an explicit
  // "Delete copy" / "Keep" pair, since a deleted copy cannot be restored.
  const [arming, setArming] = useState<number | null>(null);
  useEffect(() => {
    setArming(null);
  }, [cardId]);

  const subtitle = `${cardId} · ${copies.length} ${copies.length !== 1 ? "copies" : "copy"}`;
  const owned = price && cardId ? sumValues(copies, { [cardId]: price }) : null;
  const refState = referenceState(price);
  const game = info?.game || copies[0]?.game || "";
  const scope = game === "onepiece"
    ? (price && (price.trend_non_english > 0 || price.trend_non_english_foil > 0) ? "English and other languages" : "English")
    : "all languages";
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          {info?.name ? (
            <>
              <DialogTitle>{info.name}</DialogTitle>
              <DialogDescription className="font-mono" translate="no">{subtitle}</DialogDescription>
            </>
          ) : (
            <>
              <DialogTitle className="font-mono" translate="no">{subtitle}</DialogTitle>
              <DialogDescription className="sr-only">Copies of this card in your collection</DialogDescription>
            </>
          )}
          {info && (
            <div className="flex flex-wrap gap-1.5 pt-1">
              {info.set_name && (
                <span className="n-tag n-tag-neutral">
                  {info.set_name}
                  {info.set && <span className="font-mono n-n400">{info.set}</span>}
                </span>
              )}
              {!info.set_name && info.set && <span className="n-tag n-tag-neutral font-mono">{info.set}</span>}
              {info.number && <span className="n-tag n-tag-neutral font-mono">#{info.number}</span>}
              {info.rarity && <span className="n-tag n-tag-neutral">{info.rarity}</span>}
              {info.type && <span className="n-tag n-tag-neutral">{info.type}</span>}
              {info.colors.map((c, i) => (
                <span key={i} className="n-tag n-tag-accent">{c}</span>
              ))}
            </div>
          )}
          {price && owned && (
            <div className="flex flex-col gap-0.5 pt-2 tabular-nums">
              <p className="m-0 text-sm">{priceLine(price)}</p>
              <p className="m-0 text-sm n-n400">
                {owned.priced === 0
                  ? `Your ${copies.length === 1 ? "copy has" : "copies have"} no price for this finish`
                  : `Your ${copies.length} ${copies.length === 1 ? "copy" : "copies"}: ${formatEUR(owned.value)}` +
                    (owned.priced < owned.copies ? ` (${owned.priced} priced)` : "")}
              </p>
              <p className="m-0 text-xs n-n500">Cardmarket trend, {scope} · {formatGuideDate(price.as_of)}</p>
            </div>
          )}
          {price?.ref && props.onSetReference && (
            <p className="m-0 text-xs n-n500 pt-1">
              Priced on your Cardmarket reference.{" "}
              <button type="button" className="underline" onClick={props.onSetReference}>Change</button>
            </p>
          )}
          {props.pricesAvailable && refState !== "priced" && (
            <p className="m-0 text-sm pt-2 inline-flex items-center gap-1.5" style={{ color: refState === "unset" ? "var(--n-warn)" : "var(--n-neutral-500)" }}>
              <TriangleAlert className="w-4 h-4" aria-hidden="true" />
              {refState === "unset" ? "No Cardmarket reference, set it by hand." : "Not on Cardmarket."}
              {refState === "unset" && props.onSetReference && (
                <button type="button" className="n-btn n-btn-secondary" style={{ padding: "2px 10px" }} onClick={props.onSetReference}>
                  Set reference
                </button>
              )}
            </p>
          )}
        </DialogHeader>
        <div className="flex gap-6 min-h-0">
          <CardImage
            src={image}
            alt={info?.name || cardId || ""}
            adaptive
            className="w-72 data-[orientation=landscape]:w-[22rem] shrink-0 self-start rounded-lg"
          />
          <ul className="flex-1 overflow-y-auto overflow-x-hidden flex flex-col gap-2 list-none p-0 m-0 min-w-0" aria-label="Copies">
            {copies.map((c) => (
              <li
                key={c.id}
                className="flex items-center gap-3 rounded-lg p-2"
                style={{ border: "1px solid var(--n-divider)" }}
              >
                <button
                  className={c.foil ? "n-tag n-tag-accent" : "n-tag n-tag-neutral"}
                  aria-pressed={c.foil}
                  aria-label="Foil"
                  title={c.foil ? "Foil. Click to mark as normal." : "Normal. Click to mark as foil."}
                  onClick={() => onToggleFoil(c)}
                >
                  {c.foil && <Sparkle className="w-3 h-3" aria-hidden="true" />}
                  {c.foil ? "Foil" : "Normal"}
                </button>
                <Select value={c.lang} onValueChange={(v) => onChangeLang(c, v)}>
                  <SelectTrigger className="w-[110px] h-8 text-xs" title="Printed language" aria-label="Printed language">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {languages.map((l) => (
                      <SelectItem key={l.code} value={l.code}>{l.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {arming === c.id ? (
                  <span className="text-xs n-n400 flex-1 min-w-0">Delete this copy? It can't be restored.</span>
                ) : (
                  <>
                    <span className="text-xs n-n400 flex-1 min-w-0 truncate tabular-nums">{formatDate(c.scanned_at)}</span>
                    {/* A copy lives in its game and optionally in ONE custom
                        collection; without any custom there is nothing to pick. */}
                    {customs.length > 0 && (
                      <Select value={String(c.collection_id)} onValueChange={(v) => onMove(c.id, parseInt(v, 10) || 0)}>
                        <SelectTrigger className="w-[160px] h-8 text-xs" title="Custom collection" aria-label="Custom collection">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="0">None</SelectItem>
                          {customs.map((cc) => (
                            <SelectItem key={cc.id} value={String(cc.id)}>{cc.name}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                  </>
                )}
                {arming === c.id ? (
                  <span className="flex items-center gap-1.5 shrink-0">
                    <button
                      className="n-btn n-btn-danger n-btn-sm"
                      onClick={() => {
                        setArming(null);
                        onDelete(c.id);
                      }}
                    >
                      Delete copy
                    </button>
                    <button className="n-btn n-btn-ghost n-btn-sm" autoFocus onClick={() => setArming(null)}>
                      Keep
                    </button>
                  </span>
                ) : (
                  <button
                    className="n-btn n-btn-secondary n-btn-icon shrink-0"
                    title="Delete this copy"
                    aria-label="Delete this copy"
                    onClick={() => setArming(c.id)}
                  >
                    <Trash2 className="w-4 h-4" aria-hidden="true" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
      </DialogContent>
    </Dialog>
  );
}
