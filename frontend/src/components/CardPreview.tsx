import { ImageIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Prediction } from "@/types";

interface Props {
  locked: boolean;
  cardImage: string | null;
  prediction: Prediction | null;
  predictionError: string;
  editedCardID: string;
  onEditCardID: (id: string) => void;
}

export function CardPreview({ locked, cardImage, prediction, predictionError, editedCardID, onEditCardID }: Props) {
  return (
    <>
      {locked && (
        <Badge variant="success" className="self-start">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
          CARD DETECTED
        </Badge>
      )}

      <div className="flex items-center justify-center rounded-lg bg-muted flex-1 overflow-hidden">
        {cardImage
          ? <img src={cardImage} alt={prediction?.card_id} className="w-full h-full object-contain" />
          : <ImageIcon className="w-10 h-10 text-muted-foreground" />
        }
      </div>

      <div className="flex flex-col gap-1.5">
        {locked ? (
          <>
            <Label className="text-xs">Card ID</Label>
            <Input
              value={editedCardID}
              onChange={(e) => onEditCardID(e.target.value)}
              placeholder="e.g. ogn-001-298"
              className="text-center font-mono text-sm"
            />
            {prediction && editedCardID !== prediction.card_id && (
              <p className="text-xs text-amber-400 text-center">Overridden</p>
            )}
            {prediction && (
              <p className="text-xs text-muted-foreground text-center">
                {(prediction.confidence * 100).toFixed(1)}% confidence on {prediction.card_id}
              </p>
            )}
          </>
        ) : (
          <p className="text-center text-muted-foreground text-sm">—</p>
        )}
        {predictionError && (
          <p className="text-xs text-destructive text-center">{predictionError}</p>
        )}
      </div>
    </>
  );
}
