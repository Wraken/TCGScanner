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
import type { Collection, CollectionCard } from "@/types";

interface Props {
  cardId: string | null;              // null = closed
  copies: CollectionCard[];           // copies of that card id
  image: string | null;
  customs: Collection[];
  onClose: () => void;
  onToggleFoil: (copy: CollectionCard) => void;
  onMove: (rowId: number, customId: number) => void;
  onDelete: (rowId: number) => void;
}

export function CardCopiesDialog({
  cardId, copies, image, customs, onClose, onToggleFoil, onMove, onDelete,
}: Props) {
  return (
    <Dialog open={cardId !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="font-mono">{cardId} · {copies.length} {copies.length !== 1 ? "copies" : "copy"}</DialogTitle>
        </DialogHeader>
        <div className="flex gap-4 min-h-0">
          <div
            className="w-40 shrink-0 rounded-lg overflow-hidden self-start"
            style={{ background: "var(--n-surface)" }}
          >
            {image && <img src={image} alt={cardId ?? ""} className="w-full object-contain" />}
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
                <Select value={String(c.collection_id)} onValueChange={(v) => onMove(c.id, parseInt(v, 10) || 0)}>
                  <SelectTrigger className="w-[160px] h-8 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">no custom</SelectItem>
                    {customs.map((cc) => (
                      <SelectItem key={cc.id} value={String(cc.id)}>{cc.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
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
