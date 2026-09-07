import { Trash2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
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
import { formatDate } from "@/lib/utils";
import type { CardInfo, Collection, CollectionCard } from "@/types";

interface Props {
  cardId: string | null;              // null = closed
  copies: CollectionCard[];           // copies of that card id
  image: string | null;
  info: CardInfo | null;
  customs: Collection[];
  onClose: () => void;
  onToggleFoil: (copy: CollectionCard) => void;
  onMove: (rowId: number, customId: number) => void;
  onDelete: (rowId: number) => void;
}

export function CardCopiesDialog({
  cardId, copies, image, info, customs, onClose, onToggleFoil, onMove, onDelete,
}: Props) {
  const subtitle = `${cardId} · ${copies.length} ${copies.length !== 1 ? "copies" : "copy"}`;
  return (
    <Dialog open={cardId !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          {info?.name ? (
            <>
              <DialogTitle>{info.name}</DialogTitle>
              <p className="font-mono text-xs n-n500">{subtitle}</p>
            </>
          ) : (
            <DialogTitle className="font-mono">{subtitle}</DialogTitle>
          )}
          {info && (
            <div className="flex flex-wrap gap-1.5 pt-1">
              {info.set_name && (
                <span className="n-tag n-tag-neutral">
                  {info.set_name}
                  {info.set && <span className="font-mono n-n500">{info.set}</span>}
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
        </DialogHeader>
        <div className="flex gap-6 min-h-0">
          <div
            className="w-72 shrink-0 rounded-lg overflow-hidden self-start"
            style={{ aspectRatio: "63 / 88", background: "var(--n-surface)" }}
          >
            {image && <img src={image} alt={cardId ?? ""} className="w-full h-full object-cover" />}
          </div>
          <div className="flex-1 overflow-y-auto flex flex-col gap-2">
            {copies.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-3 rounded-lg p-2"
                style={{ border: "1px solid var(--n-divider)" }}
              >
                <button className={c.foil ? "n-tag n-tag-accent" : "n-tag n-tag-neutral"} onClick={() => onToggleFoil(c)}>
                  {c.foil ? "Foil" : "Normal"}
                </button>
                <span className="text-xs n-n500 flex-1">{formatDate(c.scanned_at)}</span>
                {/* A copy lives in its game and optionally in ONE custom
                    collection; without any custom there is nothing to pick. */}
                {customs.length > 0 && (
                  <Select value={String(c.collection_id)} onValueChange={(v) => onMove(c.id, parseInt(v, 10) || 0)}>
                    <SelectTrigger className="w-[160px] h-8 text-xs">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="0">Default only</SelectItem>
                      {customs.map((cc) => (
                        <SelectItem key={cc.id} value={String(cc.id)}>{cc.name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
                <button className="n-btn n-btn-secondary n-btn-icon" title="Delete this copy" onClick={() => onDelete(c.id)}>
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
