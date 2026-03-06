import { ImageIcon, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatDate } from "@/lib/utils";
import type { CollectionCard } from "@/types";

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cards: CollectionCard[];
  images: Record<string, string>;
  onDeleteCard: (id: number) => void;
}

export function CollectionDetail({ open, onOpenChange, cards, images, onDeleteCard }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Collection cards ({cards.length})</DialogTitle>
        </DialogHeader>
        <div className="flex-1 overflow-y-auto -mx-6 px-6">
          {cards.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              No cards in this collection.
            </p>
          ) : (
            <div className="flex flex-col gap-2">
              {cards.map((c) => (
                <div key={c.id} className="flex items-center gap-3 rounded-lg border border-border p-2">
                  <div className="w-12 h-16 rounded bg-muted flex items-center justify-center overflow-hidden shrink-0">
                    {images[c.card_id]
                      ? <img src={images[c.card_id]} alt={c.card_id} className="w-full h-full object-contain" />
                      : <ImageIcon className="w-5 h-5 text-muted-foreground" />
                    }
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-mono font-medium truncate">{c.card_id}</p>
                    <div className="flex items-center gap-2 mt-0.5">
                      <Badge variant={c.foil ? "default" : "secondary"} className="text-[10px] px-1.5 py-0">
                        {c.foil ? "Foil" : "Normal"}
                      </Badge>
                      <span className="text-[11px] text-muted-foreground">{formatDate(c.scanned_at)}</span>
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="shrink-0 h-8 w-8 text-muted-foreground hover:text-destructive"
                    onClick={() => onDeleteCard(c.id)}
                  >
                    <Trash2 className="w-4 h-4" />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
