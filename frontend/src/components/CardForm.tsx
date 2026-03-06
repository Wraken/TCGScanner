import { Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";

interface Props {
  foil: boolean;
  setFoil: (v: boolean) => void;
  editedCardID: string;
  onSave: () => void;
  onCancel: () => void;
}

export function CardForm({ foil, setFoil, editedCardID, onSave, onCancel }: Props) {
  return (
    <>
      <div className="flex flex-col gap-1.5">
        <Label className="text-xs">Finish</Label>
        <div className="flex rounded-md border border-border overflow-hidden">
          <button
            onClick={() => setFoil(false)}
            className={`flex-1 py-1.5 text-sm transition-colors ${!foil ? "bg-primary text-primary-foreground" : "bg-background text-muted-foreground hover:bg-muted"}`}
          >
            Normal
          </button>
          <button
            onClick={() => setFoil(true)}
            className={`flex-1 py-1.5 text-sm transition-colors ${foil ? "bg-primary text-primary-foreground" : "bg-background text-muted-foreground hover:bg-muted"}`}
          >
            Foil
          </button>
        </div>
      </div>

      <div className="flex gap-2 mt-auto pt-2">
        <Button variant="outline" className="flex-1" onClick={onCancel}>
          Cancel
        </Button>
        <Button className="flex-1" disabled={!editedCardID.trim()} onClick={onSave}>
          <Check className="w-4 h-4" />
          Validate
        </Button>
      </div>
    </>
  );
}
